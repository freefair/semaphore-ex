package db

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// APITokenStableIDPrefix identifies the non-secret token references exposed by
// the HTTP API. It is deliberately distinct from the random credential.
const APITokenStableIDPrefix = "semaphore-token-sha256-"

// APIToken is given to a user to allow API access
type APIToken struct {
	ID        string     `db:"id" json:"id"`
	Created   time.Time  `db:"created" json:"created"`
	Expired   bool       `db:"expired" json:"expired"`
	ExpiresAt *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	UserID    int        `db:"user_id" json:"user_id"`
	Name      string     `db:"name" json:"name"`
}

// StableID returns the public, deterministic reference for this credential.
// The credential itself remains the value used to authenticate API requests.
func (t APIToken) StableID() string {
	sum := sha256.Sum256([]byte(t.ID))
	return APITokenStableIDPrefix + hex.EncodeToString(sum[:])
}

// IsAPITokenStableID reports whether value is a canonical public token
// reference emitted by StableID.
func IsAPITokenStableID(value string) bool {
	if !strings.HasPrefix(value, APITokenStableIDPrefix) || len(value) != len(APITokenStableIDPrefix)+sha256.Size*2 {
		return false
	}

	for _, character := range value[len(APITokenStableIDPrefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}

	return true
}

// IsExpiredAt reports whether the token is revoked or past its expiry.
func (t APIToken) IsExpiredAt(now time.Time) bool {
	if t.Expired {
		return true
	}
	if t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
		return true
	}
	return false
}
