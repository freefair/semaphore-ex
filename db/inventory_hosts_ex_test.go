package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInventoryHostEventRejectsUnboundedAndInvalidData(t *testing.T) {
	for _, event := range []InventoryHostEvent{
		{Version: 2, Kind: "start"}, {Version: 1, Kind: "unknown"},
		{Version: 1, Kind: "host", Host: ""}, {Version: 1, Kind: "host", Host: "line\nbreak"},
		{Version: 1, Kind: "host", Host: strings.Repeat("a", 256)},
		{Version: 1, Kind: "complete", Count: -1}, {Version: 1, Kind: "complete", Count: 100001},
		{Version: 1, Kind: "host", Host: "web", Groups: []string{"bad\tgroup"}},
	} {
		assert.Error(t, event.Validate())
	}
	assert.NoError(t, (InventoryHostEvent{Version: 1, Kind: "host", Host: "web", Groups: []string{"all"}}).Validate())
	assert.NoError(t, (InventoryHostEvent{Version: 1, Kind: "complete", Count: 0}).Validate())
}

func TestInventoryRefreshTaskRequiresExplicitAnsibleBoolean(t *testing.T) {
	task := Task{Params: MapStringAnyField{"inventory_refresh": true}}
	assert.True(t, task.IsInventoryRefresh())
	assert.NoError(t, task.ValidateNewTask(Template{App: AppAnsible}))
	assert.Error(t, task.ValidateNewTask(Template{App: AppBash}))
	task.Params["inventory_refresh"] = "true"
	assert.False(t, task.IsInventoryRefresh())
	assert.Error(t, task.ValidateNewTask(Template{App: AppAnsible}))
	assert.False(t, (Task{}).IsInventoryRefresh())
}
