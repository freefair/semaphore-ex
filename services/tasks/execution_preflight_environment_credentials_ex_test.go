package tasks

import (
	"strconv"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionPreflightListsEnvironmentCredentialReferences(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "preflight-owner", Name: "Owner", Email: "preflight-owner@example.test"})
	require.NoError(t, err)
	credential, _, err := store.CreateGlobalCredential(
		db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "License server", OwnerUserID: owner.ID, Enabled: true, Created: time.Now().UTC()},
		db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID})
	require.NoError(t, err)
	pool := &TaskPool{store: store}
	environment := db.Environment{ID: 12, Credentials: []db.EnvironmentCredential{
		{Name: "TF_VAR_license", Target: db.EnvironmentCredentialTargetEnv, CredentialID: credential.ID},
		{Name: "missing", Target: db.EnvironmentCredentialTargetVar, CredentialID: 999_999},
	}}
	var plan pro_interfaces.ExecutionPreflightPlan
	var state []any

	pool.appendEnvironmentCredentialReferences(&plan, &state, environment)

	assert.Equal(t, []pro_interfaces.ExecutionPreflightReference{
		{Kind: pro_interfaces.ExecutionReferenceCredential, ID: credential.ID, Name: "Global credential #" + strconv.Itoa(credential.ID), Revision: "global", BindingTarget: "env:TF_VAR_license", Visible: true},
		{Kind: pro_interfaces.ExecutionReferenceCredential, ID: 999_999, Name: "Global credential #999999", Revision: "global", BindingTarget: "var:missing", Visible: true},
	}, plan.References)
	assert.Equal(t, []any{
		"environment_credential", 12, "TF_VAR_license", "env", credential.ID,
		"environment_credential", 12, "missing", "var", 999_999,
	}, state, "a changed reference must change the reviewed state")
}
