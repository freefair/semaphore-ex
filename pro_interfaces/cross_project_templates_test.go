package pro_interfaces

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateCrossProjectTemplateGrantEnforcesLifecycleScopeAndConsumerPermission(t *testing.T) {
	created := time.Now().UTC().Add(-time.Minute)
	grant := db.CrossProjectTemplateGrant{
		OwnerProjectID: 1, ConsumerProjectID: 2, TemplateID: 3,
		MinTemplateVersion: 4, MaxTemplateVersion: 6,
		Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
		Status:     db.CrossProjectTemplateGrantPending, Revision: 1,
		CreatedByUserID: 7, Created: created, Reason: "approved reference",
	}
	request := CrossProjectTemplateGrantRequest{
		ConsumerProjectID: 2, TemplateID: 3, TemplateVersion: 4,
		Operation: db.CrossProjectTemplateGrantReference, ConsumerAuthorized: true,
	}
	assert.Equal(t, CrossProjectTemplateGrantDeniedPending, EvaluateCrossProjectTemplateGrant(grant, request).Reason)
	accepted, err := grant.Accept(8, 1, time.Now().UTC())
	require.NoError(t, err)
	request.ConsumerAuthorized = false
	assert.Equal(t, CrossProjectTemplateGrantDeniedConsumerAuthorization, EvaluateCrossProjectTemplateGrant(accepted, request).Reason)
	request.ConsumerAuthorized = true
	request.TemplateVersion = 7
	assert.Equal(t, CrossProjectTemplateGrantDeniedScope, EvaluateCrossProjectTemplateGrant(accepted, request).Reason)
	request.TemplateVersion = 6
	assert.True(t, EvaluateCrossProjectTemplateGrant(accepted, request).Allowed)
	revoked, err := accepted.Revoke(7, accepted.Revision, "withdrawn", time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, CrossProjectTemplateGrantDeniedRevoked, EvaluateCrossProjectTemplateGrant(revoked, request).Reason)
}
