package tasks

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	executionPreflightReviewTokenPrefix = "ep1"
	executionPreflightReviewTokenDomain = "semaphore.execution-preflight-review:v1\x00"
	executionPreflightReviewTokenKeyMin = 32
	executionPreflightReviewTokenNonce  = 16
)

// ExecutionPreflightReviewTokenIssuer seals the minimal, value-free review
// binding in a stateless MACed envelope. It is deliberately independent from
// task JWT signing and must receive a stable application signing key so an HA
// node can verify a review issued by another node.
type ExecutionPreflightReviewTokenIssuer struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewExecutionPreflightReviewTokenIssuer constructs an issuer with a bounded
// short lifetime. The caller owns key rotation; the key is copied so changing
// the configuration buffer after construction cannot alter verification.
func NewExecutionPreflightReviewTokenIssuer(key []byte, ttl time.Duration) (*ExecutionPreflightReviewTokenIssuer, error) {
	if len(key) < executionPreflightReviewTokenKeyMin || ttl < time.Second || ttl > pro_interfaces.MaxExecutionPreflightReviewTTL {
		return nil, errors.New("execution preflight review token configuration is invalid")
	}
	return &ExecutionPreflightReviewTokenIssuer{
		key: append([]byte(nil), key...), ttl: ttl,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// Issue signs an independently verified plan fingerprint and its bounded
// component digests. It returns no decoded plan material in the token.
func (i *ExecutionPreflightReviewTokenIssuer) Issue(plan pro_interfaces.ExecutionPreflightPlan) (pro_interfaces.ExecutionPreflightReview, error) {
	return i.IssueWithComponents(plan, nil)
}

// IssueWithComponents signs the planner's private, value-derived component
// digests alongside its redacted public plan. Supplied digests replace only
// their matching bounded categories; they never enter the response DTO.
func (i *ExecutionPreflightReviewTokenIssuer) IssueWithComponents(
	plan pro_interfaces.ExecutionPreflightPlan,
	supplied map[pro_interfaces.ExecutionPreflightChangeCode]string,
) (pro_interfaces.ExecutionPreflightReview, error) {
	if i == nil || len(i.key) < executionPreflightReviewTokenKeyMin || i.ttl < time.Second || i.ttl > pro_interfaces.MaxExecutionPreflightReviewTTL {
		return pro_interfaces.ExecutionPreflightReview{}, errors.New("execution preflight review token issuer is invalid")
	}
	if err := plan.Validate(); err != nil {
		return pro_interfaces.ExecutionPreflightReview{}, errors.New("execution preflight review plan is invalid")
	}
	fingerprint, err := pro_interfaces.FingerprintExecutionPreflight(plan)
	if err != nil || !constantTimeStringEqual(plan.Fingerprint, fingerprint) {
		return pro_interfaces.ExecutionPreflightReview{}, errors.New("execution preflight review plan is invalid")
	}
	components, err := i.reviewComponents(plan, supplied)
	if err != nil {
		return pro_interfaces.ExecutionPreflightReview{}, errors.New("execution preflight review plan is invalid")
	}
	nonce := make([]byte, executionPreflightReviewTokenNonce)
	if _, err = rand.Read(nonce); err != nil {
		return pro_interfaces.ExecutionPreflightReview{}, errors.New("execution preflight review token could not be issued")
	}
	now := i.now().UTC()
	claims := pro_interfaces.ExecutionPreflightReviewTokenClaims{
		Version: pro_interfaces.ExecutionPreflightReviewTokenVersion,
		ActorID: plan.ActorID, ProjectID: plan.ProjectID, Intent: plan.Intent,
		TemplateID: plan.TemplateID, WorkflowID: plan.WorkflowID, Fingerprint: fingerprint,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(i.ttl).Unix(),
		Nonce: base64.RawURLEncoding.EncodeToString(nonce), ComponentDigests: components,
	}
	encoded, err := json.Marshal(claims)
	if err != nil || len(encoded) > pro_interfaces.MaxExecutionPreflightReviewPayloadBytes {
		return pro_interfaces.ExecutionPreflightReview{}, errors.New("execution preflight review token could not be issued")
	}
	payload := base64.RawURLEncoding.EncodeToString(encoded)
	token := executionPreflightReviewTokenPrefix + "." + payload + "." + base64.RawURLEncoding.EncodeToString(i.mac(payload))
	if len(token) > pro_interfaces.MaxExecutionPreflightReviewTokenBytes {
		return pro_interfaces.ExecutionPreflightReview{}, errors.New("execution preflight review token could not be issued")
	}
	return pro_interfaces.ExecutionPreflightReview{
		Fingerprint: fingerprint, ReviewToken: token, ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
	}, nil
}

func (i *ExecutionPreflightReviewTokenIssuer) reviewComponents(
	plan pro_interfaces.ExecutionPreflightPlan,
	supplied map[pro_interfaces.ExecutionPreflightChangeCode]string,
) ([]pro_interfaces.ExecutionPreflightComponentDigest, error) {
	components, err := pro_interfaces.ExecutionPreflightComponentDigests(plan)
	if err != nil {
		return nil, err
	}
	known := make(map[pro_interfaces.ExecutionPreflightChangeCode]int, len(components))
	for index, component := range components {
		known[component.Code] = index
	}
	for code, digest := range supplied {
		index, ok := known[code]
		if !ok || !validPreflightFingerprint(digest) {
			return nil, errors.New("execution preflight review components are invalid")
		}
		components[index].Digest = digest
	}
	// Components may be derived from sensitive effective input values. Blind
	// every digest before serializing it so a readable HMAC envelope cannot be
	// used as an offline oracle for low-entropy secret values.
	for index := range components {
		components[index].Digest = i.componentMAC(components[index].Code, components[index].Digest)
	}
	if _, err := pro_interfaces.DiffExecutionPreflightComponentDigests(components, components); err != nil {
		return nil, errors.New("execution preflight review components are invalid")
	}
	return components, nil
}

// DiffVerifiedComponents compares authenticated token component digests with
// a fresh planner snapshot. The fresh raw digests are blinded using the same
// issuer key before comparison and therefore never leave server memory.
func (i *ExecutionPreflightReviewTokenIssuer) DiffVerifiedComponents(
	claims pro_interfaces.ExecutionPreflightReviewTokenClaims,
	plan pro_interfaces.ExecutionPreflightPlan,
	components map[pro_interfaces.ExecutionPreflightChangeCode]string,
) ([]pro_interfaces.ExecutionPreflightChangeCode, error) {
	if i == nil || len(i.key) < executionPreflightReviewTokenKeyMin {
		return nil, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	fresh, err := i.reviewComponents(plan, components)
	if err != nil {
		return nil, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	changes, err := pro_interfaces.DiffExecutionPreflightComponentDigests(claims.ComponentDigests, fresh)
	if err != nil {
		return nil, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	return changes, nil
}

// Verify authenticates a review token and binds it to the authenticated route
// context plus a freshly recomputed plan. A successful verification does not
// authorize execution; callers must independently reauthorize and replan.
func (i *ExecutionPreflightReviewTokenIssuer) Verify(token string, expected pro_interfaces.ExecutionPreflightReviewBinding) (pro_interfaces.ExecutionPreflightReviewTokenClaims, error) {
	if i == nil || len(i.key) < executionPreflightReviewTokenKeyMin || expected.Validate() != nil {
		return pro_interfaces.ExecutionPreflightReviewTokenClaims{}, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	payload, encodedPayload, signature, ok := decodeExecutionPreflightReviewToken(token)
	if !ok || !hmac.Equal(i.mac(encodedPayload), signature) {
		return pro_interfaces.ExecutionPreflightReviewTokenClaims{}, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	claims, ok := parseExecutionPreflightReviewTokenClaims(payload)
	if !ok {
		return pro_interfaces.ExecutionPreflightReviewTokenClaims{}, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	now := i.now().UTC()
	if !validExecutionPreflightReviewTokenClaims(claims, now, i.ttl) {
		return pro_interfaces.ExecutionPreflightReviewTokenClaims{}, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	if !time.Unix(claims.ExpiresAt, 0).After(now) {
		return pro_interfaces.ExecutionPreflightReviewTokenClaims{}, pro_interfaces.ErrExecutionPreflightReviewTokenExpired
	}
	if !reviewClaimsMatchBinding(claims, expected) {
		return pro_interfaces.ExecutionPreflightReviewTokenClaims{}, pro_interfaces.ErrExecutionPreflightReviewScopeMismatch
	}
	return claims, nil
}

func (i *ExecutionPreflightReviewTokenIssuer) mac(payload string) []byte {
	mac := hmac.New(sha256.New, i.key)
	_, _ = mac.Write([]byte(executionPreflightReviewTokenDomain + executionPreflightReviewTokenPrefix + "." + payload))
	return mac.Sum(nil)
}

func (i *ExecutionPreflightReviewTokenIssuer) componentMAC(code pro_interfaces.ExecutionPreflightChangeCode, digest string) string {
	mac := hmac.New(sha256.New, i.key)
	_, _ = mac.Write([]byte("semaphore.execution-preflight-component:v1\x00" + string(code) + "\x00" + digest))
	return "sha256:" + hex.EncodeToString(mac.Sum(nil))
}

func decodeExecutionPreflightReviewToken(token string) ([]byte, string, []byte, bool) {
	if len(token) == 0 || len(token) > pro_interfaces.MaxExecutionPreflightReviewTokenBytes || strings.ContainsAny(token, " \t\r\n") {
		return nil, "", nil, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != executionPreflightReviewTokenPrefix || parts[1] == "" || parts[2] == "" {
		return nil, "", nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 || len(payload) > pro_interfaces.MaxExecutionPreflightReviewPayloadBytes ||
		base64.RawURLEncoding.EncodeToString(payload) != parts[1] {
		return nil, "", nil, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size || base64.RawURLEncoding.EncodeToString(signature) != parts[2] {
		return nil, "", nil, false
	}
	return payload, parts[1], signature, true
}

func parseExecutionPreflightReviewTokenClaims(payload []byte) (pro_interfaces.ExecutionPreflightReviewTokenClaims, bool) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var claims pro_interfaces.ExecutionPreflightReviewTokenClaims
	if decoder.Decode(&claims) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return pro_interfaces.ExecutionPreflightReviewTokenClaims{}, false
	}
	return claims, true
}

func validExecutionPreflightReviewTokenClaims(claims pro_interfaces.ExecutionPreflightReviewTokenClaims, now time.Time, ttl time.Duration) bool {
	if claims.Version != pro_interfaces.ExecutionPreflightReviewTokenVersion || claims.ActorID <= 0 || claims.ProjectID <= 0 ||
		(claims.Intent != pro_interfaces.ExecutionPreflightTask && claims.Intent != pro_interfaces.ExecutionPreflightWorkflow) ||
		(claims.Intent == pro_interfaces.ExecutionPreflightTask && (claims.TemplateID <= 0 || claims.WorkflowID != 0)) ||
		(claims.Intent == pro_interfaces.ExecutionPreflightWorkflow && (claims.WorkflowID <= 0 || claims.TemplateID != 0)) ||
		!validPreflightFingerprint(claims.Fingerprint) || claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > int64(pro_interfaces.MaxExecutionPreflightReviewTTL/time.Second) || claims.ExpiresAt-claims.IssuedAt > int64(ttl/time.Second) ||
		time.Unix(claims.IssuedAt, 0).After(now) || !validPreflightNonce(claims.Nonce) {
		return false
	}
	_, err := pro_interfaces.DiffExecutionPreflightComponentDigests(claims.ComponentDigests, claims.ComponentDigests)
	return err == nil
}

func reviewClaimsMatchBinding(claims pro_interfaces.ExecutionPreflightReviewTokenClaims, expected pro_interfaces.ExecutionPreflightReviewBinding) bool {
	return claims.ActorID == expected.ActorID && claims.ProjectID == expected.ProjectID && claims.Intent == expected.Intent &&
		claims.TemplateID == expected.TemplateID && claims.WorkflowID == expected.WorkflowID &&
		(expected.Fingerprint == "" || constantTimeStringEqual(claims.Fingerprint, expected.Fingerprint))
}

func validPreflightFingerprint(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validPreflightNonce(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == executionPreflightReviewTokenNonce
}

func constantTimeStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
