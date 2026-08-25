package helpers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCorrelationMiddlewareGeneratesServerOwnedIdentifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(CorrelationHeader, securityfixtures.TripwireValues[0])
	var contextID string
	handler := CorrelationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextID = CorrelationID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Len(t, contextID, 32)
	assert.Equal(t, contextID, recorder.Header().Get(CorrelationHeader))
	securityfixtures.AssertTripwiresAbsent(t, contextID, recorder.Header().Get(CorrelationHeader))
}
