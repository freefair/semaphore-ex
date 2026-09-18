package db

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
)

const (
	maxSSHKeyBindings = 64
	maxSSHKeyHosts    = 64
	maxSSHKeyJSON     = 64 * 1024
)

type SSHKeyBinding struct {
	AccessKeyID int      `json:"access_key_id"`
	Hosts       []string `json:"hosts"`
}

type SSHKeyBindings []SSHKeyBinding

func (bindings *SSHKeyBindings) Scan(value any) error {
	if value == nil {
		*bindings = nil
		return nil
	}
	var raw []byte
	switch value := value.(type) {
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	default:
		return fmt.Errorf("unsupported SSH key bindings value")
	}
	if len(raw) > maxSSHKeyJSON {
		return fmt.Errorf("SSH key bindings exceed %d bytes", maxSSHKeyJSON)
	}
	if err := json.Unmarshal(raw, bindings); err != nil {
		return err
	}
	return ValidateSSHKeyBindings(*bindings)
}

func (bindings SSHKeyBindings) Value() (driver.Value, error) {
	if bindings == nil {
		return nil, nil
	}
	if err := ValidateSSHKeyBindings(bindings); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxSSHKeyJSON {
		return nil, fmt.Errorf("SSH key bindings exceed %d bytes", maxSSHKeyJSON)
	}
	// TEXT columns are used by both SQLite and PostgreSQL. Returning a string
	// keeps the value textual instead of letting PostgreSQL infer bytea.
	return string(encoded), nil
}

func ValidateSSHKeyBindings(bindings SSHKeyBindings) error {
	if len(bindings) > maxSSHKeyBindings {
		return fmt.Errorf("SSH key bindings exceed %d", maxSSHKeyBindings)
	}
	seen := map[string]int{}
	for _, binding := range bindings {
		if binding.AccessKeyID <= 0 {
			return fmt.Errorf("SSH key binding access key ID must be positive")
		}
		if len(binding.Hosts) > maxSSHKeyHosts {
			return fmt.Errorf("SSH key binding hosts exceed %d entries", maxSSHKeyHosts)
		}
		for _, host := range binding.Hosts {
			normalized, err := normalizeSSHKeyHost(host)
			if err != nil {
				return err
			}
			if key, exists := seen[normalized]; exists && key != binding.AccessKeyID {
				return fmt.Errorf("SSH host %q is bound to multiple keys", normalized)
			}
			seen[normalized] = binding.AccessKeyID
		}
	}
	return nil
}

func ResolveTaskSSHKeys(projectDefault, projectAlways, template, task SSHKeyBindings) (SSHKeyBindings, error) {
	selected := projectDefault
	if template != nil {
		selected = template
	}
	if task != nil {
		selected = task
	}
	merged := append(copySSHKeyBindings(selected), projectAlways...)
	if err := ValidateSSHKeyBindings(merged); err != nil {
		return nil, err
	}
	byKey := map[int]map[string]struct{}{}
	for _, binding := range merged {
		if byKey[binding.AccessKeyID] == nil {
			byKey[binding.AccessKeyID] = map[string]struct{}{}
		}
		for _, host := range binding.Hosts {
			normalized, _ := normalizeSSHKeyHost(host)
			byKey[binding.AccessKeyID][normalized] = struct{}{}
		}
	}
	result := make(SSHKeyBindings, 0, len(byKey))
	for id, hosts := range byKey {
		values := make([]string, 0, len(hosts))
		for host := range hosts {
			values = append(values, host)
		}
		sort.Strings(values)
		result = append(result, SSHKeyBinding{AccessKeyID: id, Hosts: values})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AccessKeyID < result[j].AccessKeyID })
	if _, err := result.Value(); err != nil {
		return nil, err
	}
	return result, nil
}

func copySSHKeyBindings(bindings SSHKeyBindings) SSHKeyBindings {
	if bindings == nil {
		return nil
	}
	result := make(SSHKeyBindings, len(bindings))
	for i, b := range bindings {
		result[i] = SSHKeyBinding{AccessKeyID: b.AccessKeyID, Hosts: append([]string(nil), b.Hosts...)}
	}
	return result
}

func normalizeSSHKeyHost(host string) (string, error) {
	if host != strings.ToLower(host) || strings.TrimSpace(host) != host || host == "" {
		return "", fmt.Errorf("invalid SSH host %q", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	if strings.ContainsAny(host, "*@/:\\\"'\t\n\r") {
		return "", fmt.Errorf("invalid SSH host %q", host)
	}
	if len(host) > 253 || strings.Contains(host, "..") {
		return "", fmt.Errorf("invalid SSH host %q", host)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid SSH host %q", host)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", fmt.Errorf("invalid SSH host %q", host)
			}
		}
	}
	return host, nil
}
