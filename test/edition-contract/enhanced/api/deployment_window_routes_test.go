package api_test

import (
	"testing"

	"github.com/gorilla/mux"
	rootapi "github.com/semaphoreui/semaphore/api"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentWindowRoutesAreRegisteredInEnhancedRouter(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config.Debugging = &util.DebuggingConfig{}
	router := rootapi.Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, metrics.NewMetrics(), nil)
	routes := map[string]bool{}
	require.NoError(t, router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, err := route.GetPathTemplate()
		if err == nil {
			routes[template] = true
		}
		return nil
	}))
	for _, path := range []string{
		"/api/project/{project_id}/deployment-windows",
		"/api/project/{project_id}/deployment-windows/preview",
		"/api/project/{project_id}/deployment-windows/status",
		"/api/project/{project_id}/deployment-windows/history",
	} {
		assert.True(t, routes[path], path)
	}
}
