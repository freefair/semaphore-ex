package db

import (
	"time"
)

type TOTPRecoveryCode struct {
	ID         int        `db:"id"`
	TotpID     int        `db:"totp_id"`
	CodeHash   string     `db:"code_hash"`
	Created    time.Time  `db:"created"`
	ConsumedAt *time.Time `db:"consumed_at"`
}

type TOTPAttempt struct {
	UserID        int        `db:"user_id"`
	FailureCount  int        `db:"failure_count"`
	WindowStarted time.Time  `db:"window_started"`
	BlockedUntil  *time.Time `db:"blocked_until"`
	Updated       time.Time  `db:"updated"`
}

type TOTPCapabilityTransition struct {
	ID        int       `db:"id" json:"id"`
	FromState string    `db:"from_state" json:"from_state"`
	ToState   string    `db:"to_state" json:"to_state"`
	ActorID   int       `db:"actor_id" json:"actor_id"`
	Created   time.Time `db:"created" json:"created"`
}
