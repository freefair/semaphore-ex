package ssh

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildHostRoutingCountsDistinctPublicIdentities(t *testing.T) {
	routing, err := BuildHostRouting([]RoutingIdentity{
		{PublicKey: "key-a", Selector: "a.pub"},
		{PublicKey: "key-a", Selector: "a-duplicate.pub"},
		{PublicKey: "key-b", Selector: "b.pub"},
		{PublicKey: "key-c", Selector: "c.pub"},
		{PublicKey: "key-d", Selector: "d.pub"},
	})
	require.NoError(t, err)
	assert.Equal(t, 4, routing.IdentityCount())
	assert.False(t, routing.RequiresRouting())

	routing, err = BuildHostRouting(routingTestIdentities(5))
	require.NoError(t, err)
	assert.Equal(t, 5, routing.IdentityCount())
	assert.True(t, routing.RequiresRouting())
}

func TestBuildHostRoutingHonorsExplicitHostsBelowThreshold(t *testing.T) {
	routing, err := BuildHostRouting([]RoutingIdentity{
		{PublicKey: "key-a", Selector: "a.pub", Hosts: []string{"git.example.test"}},
		{PublicKey: "key-b", Selector: "b.pub"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.pub"}, routing.SelectorsForHost("git.example.test"))
	assert.Equal(t, []string{"b.pub"}, routing.SelectorsForHost("unknown.example.test"))
}

func TestBuildHostRoutingDoesNotExpandExplicitIdentityWithImplicitHost(t *testing.T) {
	identities := routingTestIdentities(5)
	identities[0].Hosts = []string{"explicit.example.test"}
	identities[0].ImplicitHosts = []string{"repository.example.test"}
	routing, err := BuildHostRouting(identities)
	require.NoError(t, err)
	assert.Empty(t, routing.SelectorsForHost("repository.example.test"))
	assert.Equal(t, []string{"selector-0.pub"}, routing.SelectorsForHost("explicit.example.test"))
}

func TestBuildHostRoutingRejectsUnsafeImplicitHost(t *testing.T) {
	identities := routingTestIdentities(5)
	identities[0].Hosts = nil
	identities[0].ImplicitHosts = []string{"unsafe host"}
	_, err := BuildHostRouting(identities)
	assert.ErrorContains(t, err, "invalid SSH routing host")
}

func TestBuildHostRoutingUsesRepositoryRouteOnlyWhenRequired(t *testing.T) {
	identities := routingTestIdentities(5)
	identities[0].Hosts = nil
	identities[0].ImplicitHosts = []string{"repo.example.test"}
	routing, err := BuildHostRouting(identities)
	require.NoError(t, err)
	assert.Equal(t, []string{"selector-0.pub"}, routing.SelectorsForHost("repo.example.test"))
	assert.Empty(t, routing.SelectorsForHost("unknown.example.test"))
	config := filepath.Join(t.TempDir(), "routing.conf")
	require.NoError(t, os.WriteFile(config, []byte(routing.Config("agent.sock")), 0o600))
	unknown, err := exec.Command("ssh", "-G", "-F", config, "unknown.example.test").Output()
	require.NoError(t, err)
	assert.NotContains(t, string(unknown), "identityfile selector-0.pub")
	assert.Contains(t, string(unknown), "identityfile none")
}

func TestBuildHostRoutingExplicitHostOverridesImplicitRoute(t *testing.T) {
	routing, err := BuildHostRouting([]RoutingIdentity{
		{PublicKey: "repo", Selector: "repo.pub", ImplicitHosts: []string{"git.example.test"}},
		{PublicKey: "extra", Selector: "extra.pub", Hosts: []string{"git.example.test"}},
		{PublicKey: "c", Selector: "c.pub", Hosts: []string{"c.example.test"}},
		{PublicKey: "d", Selector: "d.pub", Hosts: []string{"d.example.test"}},
		{PublicKey: "e", Selector: "e.pub", Hosts: []string{"e.example.test"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"extra.pub"}, routing.SelectorsForHost("git.example.test"))
}

func TestBuildHostRoutingRejectsConflictingExplicitHosts(t *testing.T) {
	_, err := BuildHostRouting([]RoutingIdentity{
		{PublicKey: "a", Selector: "a.pub", Hosts: []string{"git.example.test"}},
		{PublicKey: "b", Selector: "b.pub", Hosts: []string{"git.example.test"}},
	})
	assert.ErrorContains(t, err, "multiple SSH identities")
}

func TestBuildHostRoutingRequiresEveryIdentityToBeMappedAtThreshold(t *testing.T) {
	identities := routingTestIdentities(5)
	identities[0].Hosts = nil
	_, err := BuildHostRouting(identities)
	assert.ErrorContains(t, err, "requires an explicit host route")
}

func TestHostRoutingConfigQuotesSelectorPathsForOpenSSH(t *testing.T) {
	routing, err := BuildHostRouting([]RoutingIdentity{{PublicKey: "key-a", Selector: `/tmp/selector \\ "quotes" $literal.pub`, Hosts: []string{"git.example.test"}}})
	require.NoError(t, err)
	config := filepath.Join(t.TempDir(), "routing.conf")
	require.NoError(t, os.WriteFile(config, []byte(routing.Config(`/tmp/agent \\ "socket" $literal`)), 0o600))
	output, err := exec.Command("ssh", "-G", "-F", config, "git.example.test").Output()
	require.NoError(t, err)
	text := string(output)
	assert.Contains(t, text, `identityfile /tmp/selector \\ "quotes" $literal.pub`)
	assert.Contains(t, text, `identityagent /tmp/agent \\ "socket" $literal`)
}

func TestHostRoutingConfigDoesNotOfferScopedFallbackSelector(t *testing.T) {
	routing, err := BuildHostRouting([]RoutingIdentity{
		{PublicKey: "scoped", Selector: "scoped.pub", Hosts: []string{"git.example.test"}},
		{PublicKey: "unscoped", Selector: "unscoped.pub"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"unscoped.pub"}, routing.SelectorsForHost("elsewhere.example.test"))
	config := routing.Config("agent.sock")
	assert.Contains(t, config, `IdentityFile "unscoped.pub"`)
	assert.Contains(t, config, "Match final host *,!git.example.test")
}

func TestHostRoutingNativeConfigUsesExactAndNegativeHostPatterns(t *testing.T) {
	routing, err := BuildHostRouting([]RoutingIdentity{
		{PublicKey: "scoped", Selector: "scoped.pub", Hosts: []string{"git.example.test"}},
		{PublicKey: "unscoped", Selector: "unscoped.pub"},
	})
	require.NoError(t, err)
	config := filepath.Join(t.TempDir(), "routing.conf")
	require.NoError(t, os.WriteFile(config, []byte(routing.Config("agent.sock")), 0o600))
	mapped, err := exec.Command("ssh", "-G", "-F", config, "Git.Example.Test").Output()
	require.NoError(t, err)
	assert.Contains(t, string(mapped), "identityfile scoped.pub")
	assert.NotContains(t, string(mapped), "identityfile unscoped.pub")
	unknown, err := exec.Command("ssh", "-G", "-F", config, "unknown.example.test").Output()
	require.NoError(t, err)
	assert.Contains(t, string(unknown), "identityfile unscoped.pub")
	assert.NotContains(t, string(unknown), "identityfile scoped.pub")
}

func routingTestIdentities(count int) []RoutingIdentity {
	identities := make([]RoutingIdentity, count)
	for index := range identities {
		identities[index] = RoutingIdentity{PublicKey: "key-" + string(rune('a'+index)), Selector: "selector-" + string(rune('0'+index)) + ".pub", Hosts: []string{"host-" + string(rune('a'+index)) + ".example.test"}}
	}
	return identities
}
