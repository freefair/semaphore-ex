package tasks

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionPreflightReviewTokenRoundTripBindsValueFreePlan(t *testing.T) {
	issuer := newExecutionPreflightReviewTokenIssuer(t, time.Minute)
	plan := executionPreflightTokenPlan(t)
	review, err := issuer.Issue(plan)
	require.NoError(t, err)
	assert.Equal(t, plan.Fingerprint, review.Fingerprint)
	assert.Equal(t, issuer.now().Add(time.Minute), review.ExpiresAt)
	assert.NotContains(t, review.ReviewToken, "secret-value-must-not-enter-token")

	claims, err := issuer.Verify(review.ReviewToken, pro_interfaces.ExecutionPreflightReviewBindingFromPlan(plan))
	require.NoError(t, err)
	assert.Equal(t, plan.Fingerprint, claims.Fingerprint)
	assert.Len(t, claims.ComponentDigests, 7)
	assert.Equal(t, 16, decodedPreflightNonceLength(t, claims.Nonce))
}

func TestExecutionPreflightReviewTokenRejectsTamperingExpiryAndScopeMismatch(t *testing.T) {
	issuer := newExecutionPreflightReviewTokenIssuer(t, time.Minute)
	plan := executionPreflightTokenPlan(t)
	review, err := issuer.Issue(plan)
	require.NoError(t, err)
	binding := pro_interfaces.ExecutionPreflightReviewBindingFromPlan(plan)

	tampered := review.ReviewToken[:len(review.ReviewToken)-1] + "x"
	_, err = issuer.Verify(tampered, binding)
	assert.ErrorIs(t, err, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid)

	binding.ActorID++
	_, err = issuer.Verify(review.ReviewToken, binding)
	assert.ErrorIs(t, err, pro_interfaces.ErrExecutionPreflightReviewScopeMismatch)

	issuer.now = func() time.Time { return review.ExpiresAt }
	_, err = issuer.Verify(review.ReviewToken, pro_interfaces.ExecutionPreflightReviewBindingFromPlan(plan))
	assert.ErrorIs(t, err, pro_interfaces.ErrExecutionPreflightReviewTokenExpired)
}

func TestExecutionPreflightReviewTokenRejectsInvalidConfigurationAndUnsignedFingerprint(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	_, err := NewExecutionPreflightReviewTokenIssuer(key[:31], time.Minute)
	assert.Error(t, err)
	_, err = NewExecutionPreflightReviewTokenIssuer(key, pro_interfaces.MaxExecutionPreflightReviewTTL+time.Second)
	assert.Error(t, err)

	issuer := newExecutionPreflightReviewTokenIssuer(t, time.Minute)
	plan := executionPreflightTokenPlan(t)
	plan.Fingerprint = "sha256:" + strings.Repeat("0", 64)
	_, err = issuer.Issue(plan)
	assert.Error(t, err)
}

func TestExecutionPreflightComponentDigestsReportOnlyStableChanges(t *testing.T) {
	before := executionPreflightTokenPlan(t)
	after := executionPreflightTokenPlan(t)
	after.Inputs[0].Present = false
	beforeDigests, err := pro_interfaces.ExecutionPreflightComponentDigests(before)
	require.NoError(t, err)
	afterDigests, err := pro_interfaces.ExecutionPreflightComponentDigests(after)
	require.NoError(t, err)
	changes, err := pro_interfaces.DiffExecutionPreflightComponentDigests(beforeDigests, afterDigests)
	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.ExecutionPreflightChangeCode{pro_interfaces.ExecutionChangeInput}, changes)

	_, err = pro_interfaces.DiffExecutionPreflightComponentDigests(beforeDigests[:6], afterDigests)
	assert.Error(t, err)
}

func TestExecutionPreflightReviewTokenKeepsPrivateComponentDigestsOutOfPlan(t *testing.T) {
	issuer := newExecutionPreflightReviewTokenIssuer(t, time.Minute)
	plan := executionPreflightTokenPlan(t)
	privateDigest := "sha256:" + strings.Repeat("c", 64)
	review, err := issuer.IssueWithComponents(plan, map[pro_interfaces.ExecutionPreflightChangeCode]string{
		pro_interfaces.ExecutionChangeInput: privateDigest,
	})
	require.NoError(t, err)
	claims, err := issuer.Verify(review.ReviewToken, pro_interfaces.ExecutionPreflightReviewBindingFromPlan(plan))
	require.NoError(t, err)
	for _, component := range claims.ComponentDigests {
		if component.Code == pro_interfaces.ExecutionChangeInput {
			assert.NotEqual(t, privateDigest, component.Digest)
		}
	}
	changes, err := issuer.DiffVerifiedComponents(claims, plan, map[pro_interfaces.ExecutionPreflightChangeCode]string{
		pro_interfaces.ExecutionChangeInput: privateDigest,
	})
	require.NoError(t, err)
	assert.Empty(t, changes)
	assert.NotContains(t, review.Fingerprint, privateDigest)
}

func TestExecutionPreflightReviewTokenSupportsBoundedStaleDiffWithoutClientFingerprint(t *testing.T) {
	issuer := newExecutionPreflightReviewTokenIssuer(t, time.Minute)
	previous := executionPreflightTokenPlan(t)
	review, err := issuer.Issue(previous)
	require.NoError(t, err)
	binding := pro_interfaces.ExecutionPreflightReviewBindingFromPlan(previous)
	binding.Fingerprint = ""
	claims, err := issuer.Verify(review.ReviewToken, binding)
	require.NoError(t, err)

	current := executionPreflightTokenPlan(t)
	current.Inputs[0].Present = false
	current.Fingerprint, err = pro_interfaces.FingerprintExecutionPreflight(current)
	require.NoError(t, err)
	changes, err := issuer.DiffVerifiedComponents(claims, current, nil)
	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.ExecutionPreflightChangeCode{pro_interfaces.ExecutionChangeInput}, changes)
}

func newExecutionPreflightReviewTokenIssuer(t *testing.T, ttl time.Duration) *ExecutionPreflightReviewTokenIssuer {
	t.Helper()
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), ttl)
	require.NoError(t, err)
	issuer.now = func() time.Time { return time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC) }
	return issuer
}

func executionPreflightTokenPlan(t *testing.T) pro_interfaces.ExecutionPreflightPlan {
	t.Helper()
	plan := pro_interfaces.ExecutionPreflightPlan{
		ContractVersion: pro_interfaces.ExecutionPreflightContractVersion,
		Intent:          pro_interfaces.ExecutionPreflightTask,
		ProjectID:       7,
		ActorID:         9,
		TemplateID:      11,
		Definition: pro_interfaces.ExecutionPreflightDefinition{
			Kind: pro_interfaces.ExecutionReferenceTemplate,
			ID:   11, Name: "secret-value-must-not-enter-token", Revision: "3",
			Fingerprint: "sha256:" + strings.Repeat("a", 64),
		},
		Inputs: []pro_interfaces.ExecutionPreflightInput{{Name: "deploy_token", Type: "string", Source: "credential", Present: true, Sensitive: true}},
		Placements: []pro_interfaces.ExecutionPreflightPlacement{{
			Provisional: true, Decision: pro_interfaces.ExecutionReasonSelected,
		}},
	}
	fingerprint, err := pro_interfaces.FingerprintExecutionPreflight(plan)
	require.NoError(t, err)
	plan.Fingerprint = fingerprint
	return plan
}

func decodedPreflightNonceLength(t *testing.T, nonce string) int {
	t.Helper()
	value, err := base64.RawURLEncoding.DecodeString(nonce)
	require.NoError(t, err)
	return len(value)
}
