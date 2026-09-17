package jwt

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAudience_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		audience Audience
		expected string
	}{
		{"empty", Audience{}, "null"},
		{"nil", nil, "null"},
		{"single", Audience{"semaphore"}, `"semaphore"`},
		{"multiple", Audience{"a", "b"}, `["a","b"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.audience)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, string(b))
		})
	}
}

func TestAudience_IsZero(t *testing.T) {
	assert.True(t, Audience(nil).IsZero())
	assert.True(t, Audience{}.IsZero())
	assert.False(t, Audience{"x"}.IsZero())
}

func TestAudience_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected Audience
		valid    bool
	}{
		{"null", "null", nil, true},
		{"scalar", `"semaphore"`, Audience{"semaphore"}, true},
		{"array", `["a","b"]`, Audience{"a", "b"}, true},
		{"empty array", `[]`, Audience{}, true},
		{"object", `{}`, nil, false},
		{"number", `1`, nil, false},
		{"null element", `[null]`, nil, false},
		{"number element", `[1]`, nil, false},
		{"malformed", `[`, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var audience Audience
			err := json.Unmarshal([]byte(tt.input), &audience)
			if tt.valid {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, audience)
				return
			}
			assert.Error(t, err)
		})
	}
}
