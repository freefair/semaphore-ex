package api

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/services"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionAuthenticationHonorsConfiguredInactivity(t *testing.T) {
	store := setupSessionTest(t)
	util.Config.Auth.SessionInactivityHours = 1
	user := createUserOptionsTestUser(t, store, "idle-session")
	for _, check := range []struct {
		name         string
		authenticate func(db.Session) bool
	}{
		{"api", func(session db.Session) bool {
			_, ok := getSession(sessionRequest(t, store, session, audit.AuthMethodMetadata{}, ""))
			return ok
		}},
		{"service", func(session db.Session) bool {
			request := sessionRequest(t, store, session, audit.AuthMethodMetadata{}, "")
			cookie, err := request.Cookie("semaphore")
			require.NoError(t, err)
			_, ok := services.NewSessionService(store).GetSession(*cookie)
			return ok
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				idle    time.Duration
				allowed bool
			}{{"active", 30 * time.Minute, true}, {"expired", 2 * time.Hour, false}} {
				t.Run(tc.name, func(t *testing.T) {
					now := tz.Now()
					session, err := store.CreateSession(db.Session{UserID: user.ID, Created: now.Add(-3 * time.Hour), LastActive: now.Add(-tc.idle)})
					require.NoError(t, err)
					assert.Equal(t, tc.allowed, check.authenticate(session))
					_, err = store.GetSession(user.ID, session.ID)
					if tc.allowed {
						assert.NoError(t, err)
					} else {
						assert.Error(t, err)
						assert.False(t, check.authenticate(session))
					}
				})
			}
		})
	}
}
