package db_test

import (
	"math"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
)

func TestDirectOverflowSessionLifetimeStillExpires(t *testing.T) {
	now := time.Date(2500, time.January, 1, 0, 0, 0, 0, time.UTC)
	overflowingHours := int(int64(math.MaxInt64)/int64(time.Hour) + 1)
	maxLife := (&util.ConfigType{Auth: &util.AuthConfig{MaxSessionLifeHours: overflowingHours}}).MaxSessionLife()
	session := db.Session{Created: now.Add(-maxLife).Add(-time.Hour), LastActive: now}

	assert.True(t, session.IsExpiredAt(now, maxLife, db.SessionInactivityTimeout))
}
