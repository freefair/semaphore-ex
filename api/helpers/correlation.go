package helpers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const CorrelationHeader = "X-Request-ID"

type correlationContextKey struct{}

func CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := newCorrelationID()
		w.Header().Set(CorrelationHeader, correlationID)
		ctx := context.WithValue(r.Context(), correlationContextKey{}, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func CorrelationID(ctx context.Context) string {
	correlationID, _ := ctx.Value(correlationContextKey{}).(string)
	return correlationID
}

func newCorrelationID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic("cannot generate request correlation ID")
	}
	return hex.EncodeToString(bytes)
}
