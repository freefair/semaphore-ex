package api

import (
	"bytes"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCsrfDenialLogDoesNotIncludeRequestHeaders(t *testing.T) {
	var output bytes.Buffer
	logger := log.StandardLogger()
	previousOutput := logger.Out
	logger.SetOutput(&output)
	defer logger.SetOutput(previousOutput)
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/lifecycle-test/records", nil)
	request.Host = "semaphore.example.com"
	request.Header.Set("Origin", "https://"+securityfixtures.TripwireValues[0]+".example")
	recorder := httptest.NewRecorder()

	csrfProtectionMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cross-origin request must not be forwarded")
	})).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	securityfixtures.AssertTripwiresAbsent(t, recorder.Body.String(), output.String())
}
