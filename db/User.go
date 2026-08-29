package db

import (
	"time"

	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

// User is the model for an entity which has access to the API
type User struct {
	ID       int       `db:"id" json:"id"`
	Created  time.Time `db:"created" json:"created"`
	Username string    `db:"username" json:"username" binding:"required"`
	Name     string    `db:"name" json:"name" binding:"required"`
	Email    string    `db:"email" json:"email" binding:"required"`
	Password string    `db:"password" json:"-"` // password hash
	Admin    bool      `db:"admin" json:"admin"`
	External bool      `db:"external" json:"external"`
	Alert    bool      `db:"alert" json:"alert"`
	Pro      bool      `db:"pro" json:"pro"`

	Totp     *UserTotp     `db:"-" json:"totp,omitempty"`
	EmailOtp *UserEmailOtp `db:"-" json:"email_otp,omitempty"`
}

type UserTotp struct {
	ID                     int        `db:"id" json:"id"`
	Created                time.Time  `db:"created" json:"created"`
	UserID                 int        `db:"user_id" json:"user_id"`
	URL                    string     `db:"url" json:"-"`
	RecoveryHash           string     `db:"recovery_hash" json:"-"`
	EncryptedSecret        string     `db:"encrypted_secret" json:"-"`
	State                  string     `db:"state" json:"state"`
	ConfirmedAt            *time.Time `db:"confirmed_at" json:"confirmed_at,omitempty"`
	RecoveryAcknowledgedAt *time.Time `db:"recovery_acknowledged_at" json:"recovery_acknowledged_at,omitempty"`
	ExpiresAt              *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	LastUsedStep           *int64     `db:"last_used_step" json:"-"`
}

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

type UserEmailOtp struct {
	ID       int       `db:"id" json:"id"`
	Created  time.Time `db:"created" json:"created"`
	UserID   int       `db:"user_id" json:"user_id"`
	Code     string    `db:"code" json:"code"`
	Attempts int       `db:"attempts" json:"attempts"`
}

const EmailOtpMaxAttempts = 5

type UserWithProjectRole struct {
	Role     ProjectUserRole `db:"role" json:"role"`
	RoleID   *ProjectRoleID  `db:"role_id" json:"role_id,omitempty"`
	Revision int             `db:"revision" json:"revision"`
	User
}

// UserWithPwd extends User structure with field for unhashed password received from JSON.
type UserWithPwd struct {
	Pwd string `db:"-" json:"password"` // unhashed password from JSON
	User
}

func ValidateUser(user User) error {
	if user.Username == "" {
		return &common_errors.ValidationError{Message: "Username cannot be empty"}
	}
	if user.Email == "" {
		return &common_errors.ValidationError{Message: "Email cannot be empty"}
	}
	if user.Name == "" {
		return &common_errors.ValidationError{Message: "Name cannot be empty"}
	}
	return nil
}

func (o *UserEmailOtp) IsExpired() bool {
	// Email OTP is valid for 10 minutes
	return tz.Now().Sub(o.Created) > 10*time.Minute
}
