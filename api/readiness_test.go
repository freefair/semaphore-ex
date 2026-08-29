package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadinessHandlerKeepsCommunityReady(t *testing.T) {
	util.Config = &util.ConfigType{HA: nil}
	recorder := httptest.NewRecorder()
	readinessHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/ready", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var result pro_interfaces.ClusterServiceReadiness
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	assert.True(t, result.Ready)
	assert.True(t, result.AcceptingCoordinatedWork)
}

func TestReadinessHandlerSeparatesRedisDegradationFromTrafficReadiness(t *testing.T) {
	util.Config = &util.ConfigType{HA: &util.HAConfig{Enabled: true}}
	request := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	request = helpers.SetContextValue(request, "cluster_inspector", readinessProviderFake{
		Ready: true, AcceptingCoordinatedWork: false,
		State: pro_interfaces.ClusterServiceDegradedLiveEvents,
	})
	recorder := httptest.NewRecorder()

	readinessHandler(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"accepting_coordinated_work":false`)
	assert.Contains(t, recorder.Body.String(), `"state":"degraded_live_events"`)
}

func TestReadinessHandlerRejectsDrainingOrMissingEnhancedProvider(t *testing.T) {
	util.Config = &util.ConfigType{HA: &util.HAConfig{Enabled: true}}
	for name, provider := range map[string]any{
		"draining": readinessProviderFake{State: pro_interfaces.ClusterServiceReadinessState(pro_interfaces.ClusterNodeDraining)},
		"missing":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
			if provider != nil {
				request = helpers.SetContextValue(request, "cluster_inspector", provider)
			}
			recorder := httptest.NewRecorder()
			readinessHandler(recorder, request)
			assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		})
	}
}

type readinessProviderFake pro_interfaces.ClusterServiceReadiness

func (f readinessProviderFake) Readiness() pro_interfaces.ClusterServiceReadiness {
	return pro_interfaces.ClusterServiceReadiness(f)
}
