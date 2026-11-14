package projects

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

func TestTeamUserLabel(t *testing.T) {
	tests := []struct {
		name     string
		user     db.User
		expected string
	}{
		{"name and username", db.User{ID: 4, Name: "Sam Weber", Username: "sam"}, "Sam Weber (sam)"},
		{"username only", db.User{ID: 4, Username: "sam"}, "sam"},
		{"name only", db.User{ID: 4, Name: "Sam Weber"}, "Sam Weber"},
		{"neither", db.User{ID: 4}, "User #4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, teamUserLabel(tt.user))
		})
	}
}
