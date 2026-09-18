package ssh

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// RoutingIdentity describes one selector in a task SSH agent. PublicKey must
// be a stable public-key fingerprint, never private key material. Hosts are
// administrator-selected routes; ImplicitHosts are repository-derived routes
// that are used only when the task has five or more distinct identities.
type RoutingIdentity struct {
	PublicKey     string
	Selector      string
	Hosts         []string
	ImplicitHosts []string
}

// HostRouting is the shared task-agent selection policy for local and
// container executors. It intentionally provides selectors, not credentials.
type HostRouting struct {
	identityCount     int
	fallbackSelectors []string
	explicit          map[string]string
	implicit          map[string]string
}

// BuildHostRouting deduplicates by public identity, merges routes for duplicate
// credentials, and rejects ambiguous explicit routes. Explicit routes always
// take precedence over repository-derived implicit routes.
func BuildHostRouting(identities []RoutingIdentity) (HostRouting, error) {
	routing := HostRouting{explicit: map[string]string{}, implicit: map[string]string{}}
	byPublicKey := make(map[string]*RoutingIdentity, len(identities))
	orderedPublicKeys := make([]string, 0, len(identities))
	for _, identity := range identities {
		if identity.PublicKey == "" || identity.Selector == "" {
			return HostRouting{}, fmt.Errorf("SSH routing identity requires public key and selector")
		}
		current, exists := byPublicKey[identity.PublicKey]
		if !exists {
			copy := RoutingIdentity{PublicKey: identity.PublicKey, Selector: identity.Selector}
			byPublicKey[identity.PublicKey] = &copy
			orderedPublicKeys = append(orderedPublicKeys, identity.PublicKey)
			current = &copy
		}
		current.Hosts = append(current.Hosts, identity.Hosts...)
		current.ImplicitHosts = append(current.ImplicitHosts, identity.ImplicitHosts...)
	}
	routing.identityCount = len(orderedPublicKeys)
	for _, publicKey := range orderedPublicKeys {
		identity := byPublicKey[publicKey]
		for _, host := range identity.Hosts {
			host, err := normalizeRoutingHost(host)
			if err != nil {
				return HostRouting{}, err
			}
			if existing, exists := routing.explicit[host]; exists && existing != identity.Selector {
				return HostRouting{}, fmt.Errorf("SSH host %q is routed to multiple SSH identities", host)
			}
			routing.explicit[host] = identity.Selector
		}
		if len(identity.Hosts) == 0 {
			routing.fallbackSelectors = append(routing.fallbackSelectors, identity.Selector)
		}
	}
	if routing.RequiresRouting() {
		for _, publicKey := range orderedPublicKeys {
			identity := byPublicKey[publicKey]
			if len(identity.Hosts) > 0 {
				continue
			}
			for _, host := range identity.ImplicitHosts {
				host, err := normalizeRoutingHost(host)
				if err != nil {
					return HostRouting{}, err
				}
				if _, explicit := routing.explicit[host]; explicit {
					continue
				}
				if existing, exists := routing.implicit[host]; exists && existing != identity.Selector {
					return HostRouting{}, fmt.Errorf("SSH host %q is routed to multiple repository identities", host)
				}
				routing.implicit[host] = identity.Selector
			}
		}
		for _, publicKey := range orderedPublicKeys {
			identity := byPublicKey[publicKey]
			if len(identity.Hosts) == 0 && len(identity.ImplicitHosts) == 0 {
				return HostRouting{}, fmt.Errorf("SSH identity %q requires an explicit host route when five or more identities are selected", identity.Selector)
			}
		}
	}
	return routing, nil
}

func (routing HostRouting) IdentityCount() int { return routing.identityCount }

func (routing HostRouting) RequiresRouting() bool { return routing.identityCount >= 5 }

// SelectorsForHost returns exactly the selector(s) which may be offered to a
// host. An unmapped host falls back to all keys only below the product routing
// threshold; at or above it no selector is offered.
func (routing HostRouting) SelectorsForHost(host string) []string {
	host, err := normalizeRoutingHost(host)
	if err != nil {
		return nil
	}
	if selector, exists := routing.explicit[host]; exists {
		return []string{selector}
	}
	if selector, exists := routing.implicit[host]; exists {
		return []string{selector}
	}
	if routing.RequiresRouting() {
		return nil
	}
	return append([]string(nil), routing.fallbackSelectors...)
}

// RoutedHosts returns the hostnames that require an exact selector.
func (routing HostRouting) RoutedHosts() []string {
	hosts := make([]string, 0, len(routing.explicit)+len(routing.implicit))
	for host := range routing.explicit {
		hosts = append(hosts, host)
	}
	for host := range routing.implicit {
		if _, explicit := routing.explicit[host]; !explicit {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// Config renders one native OpenSSH configuration. Match final performs a
// second pass after OpenSSH canonicalizes the requested hostname, including
// mixed-case DNS input. Negative host patterns keep fallback identities off
// every explicitly routed host, so no wrapper needs to interpret SSH syntax.
func (routing HostRouting) Config(socket string) string {
	var config strings.Builder
	hosts := routing.RoutedHosts()
	for _, host := range hosts {
		config.WriteString("Match final host ")
		config.WriteString(host)
		config.WriteString("\n  IdentityFile ")
		config.WriteString(sshConfigValue(routing.SelectorsForHost(host)[0]))
		config.WriteString("\nMatch all\n")
	}
	if !routing.RequiresRouting() && len(routing.fallbackSelectors) > 0 {
		config.WriteString("Match final host *")
		for _, host := range hosts {
			config.WriteString(",!")
			config.WriteString(host)
		}
		config.WriteString("\n")
		for _, selector := range routing.fallbackSelectors {
			config.WriteString("  IdentityFile ")
			config.WriteString(sshConfigValue(selector))
			config.WriteString("\n")
		}
	}
	config.WriteString("Match all\n")
	config.WriteString("Host *\n  IdentitiesOnly yes\n  IdentityAgent ")
	config.WriteString(sshConfigValue(socket))
	config.WriteString("\n  IdentityFile none\n")
	return config.String()
}

func normalizeRoutingHost(host string) (string, error) {
	if host != strings.ToLower(host) || strings.TrimSpace(host) != host || host == "" {
		return "", fmt.Errorf("invalid SSH routing host %q", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	if strings.ContainsAny(host, "*@/:\\\"'\t\n\r") || len(host) > 253 || strings.Contains(host, "..") {
		return "", fmt.Errorf("invalid SSH routing host %q", host)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid SSH routing host %q", host)
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return "", fmt.Errorf("invalid SSH routing host %q", host)
			}
		}
	}
	return host, nil
}

func sshConfigValue(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), `"`, `\"`) + `"`
}
