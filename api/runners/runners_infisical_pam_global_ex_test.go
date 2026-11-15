package runners

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

func TestGlobalInfisicalPAMNeedsExplicitRunnerTrust(t *testing.T) {
	route := []db.InfisicalPAMRoute{{Host: "web-1", Account: "servers/web-1"}}
	tests := []struct {
		name    string
		configs []db.TaskInfisicalPAM
		blocked bool
	}{
		{"project storage", []db.TaskInfisicalPAM{{StorageID: 1, Routes: route}}, false},
		{"global storage without trust", []db.TaskInfisicalPAM{{StorageID: 2, Global: true, StorageName: "Company", Routes: route}}, true},
		{"global storage with trust", []db.TaskInfisicalPAM{{StorageID: 2, Global: true, AllowRemoteRunners: true, Routes: route}}, false},
		{"global storage without routes", []db.TaskInfisicalPAM{{StorageID: 2, Global: true}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, blocked := globalInfisicalPAMWithoutRunnerTrust(tt.configs)
			assert.Equal(t, tt.blocked, blocked)
			if blocked {
				assert.Equal(t, "Company", name)
			}
		})
	}
}
