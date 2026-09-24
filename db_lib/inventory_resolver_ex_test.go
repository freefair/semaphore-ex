package db_lib

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventoryResolverSeparatesMembershipFromExecutionAndSecrets(t *testing.T) {
	python, err := exec.LookPath("python3")
	require.NoError(t, err)
	root := t.TempDir()
	resolver, err := installInventoryResolver(root)
	require.NoError(t, err)
	marker := filepath.Join(root, "playbook-ran")
	inventory := "#!" + python + `
import json, os, sys
assert '--limit' not in sys.argv
assert '--tags' not in sys.argv
assert '--list' in sys.argv
assert '--playbook-dir' in sys.argv
assert 'SEMAPHORE_INVENTORY_VAULT_INPUTS' not in os.environ
for arg in sys.argv:
    if arg.startswith('--vault-id='):
        filename = arg.split('@', 1)[1]
        assert open(filename).read() == 'vault-fixture-secret'
        assert os.stat(filename).st_mode & 0o777 == 0o600
print('password=raw-inventory-secret', file=sys.stderr)
print(json.dumps({'_meta': {'hostvars': {'web': {'password': 'raw-inventory-secret'}}}, 'all': {'children': ['webservers']}, 'webservers': {'hosts': ['WEB.', 'never-run']}}))
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "ansible-inventory"), []byte(inventory), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ansible-playbook"), []byte("#!"+python+"\nimport os\nopen(os.environ['TEST_MARKER'], 'w').write('ran')\nprint('playbook output')\n"), 0700))
	for _, mode := range []string{"refresh", "run"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(python, resolver, mode, "--inventory", "hosts.yml", "--limit", "web", "--tags", "deploy", "--vault-id=main@prompt", "site.yml")
			command.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_MARKER="+marker, `SEMAPHORE_INVENTORY_VAULT_INPUTS={"Vault password (main):":"vault-fixture-secret"}`)
			output, runErr := command.CombinedOutput()
			require.NoError(t, runErr, string(output))
			assert.NotContains(t, string(output), "raw-inventory-secret")
			assert.NotContains(t, string(output), "vault-fixture-secret")
			var hosts []db.InventoryHostEvent
			for _, line := range strings.Split(string(output), "\n") {
				if !strings.HasPrefix(line, "SEMAPHORE_INVENTORY_RESULT ") {
					continue
				}
				var event db.InventoryHostEvent
				require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "SEMAPHORE_INVENTORY_RESULT ")), &event))
				require.NoError(t, event.Validate())
				if event.Kind == "host" {
					hosts = append(hosts, event)
				}
			}
			require.Len(t, hosts, 2)
			assert.Equal(t, "never-run", hosts[0].Host)
			assert.Equal(t, "web", hosts[1].Host)
			assert.Equal(t, []string{"all", "webservers"}, hosts[1].Groups)
			_, statErr := os.Stat(marker)
			if mode == "refresh" {
				assert.True(t, os.IsNotExist(statErr), "refresh must not execute playbook")
			} else {
				require.NoError(t, statErr)
			}
		})
	}
}

func TestInventoryResolverFailureDoesNotExecuteRefreshPlaybook(t *testing.T) {
	python, err := exec.LookPath("python3")
	require.NoError(t, err)
	root := t.TempDir()
	resolver, err := installInventoryResolver(root)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "ansible-inventory"), []byte("#!"+python+"\nimport sys\nprint('private secret', file=sys.stderr)\nsys.exit(1)\n"), 0700))
	command := exec.Command(python, resolver, "refresh", "--inventory", "hosts.yml", "site.yml")
	command.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(output), `"event":"error"`)
	assert.NotContains(t, string(output), "private secret")
	assert.NotContains(t, string(output), `"event":"complete"`)
}
