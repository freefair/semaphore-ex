package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	storepkg "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeGlobalCredentialCipher struct {
	value []byte
	err   error
}

func (c runtimeGlobalCredentialCipher) OptionEncryptionEnabled() bool { return true }
func (c runtimeGlobalCredentialCipher) DecryptOption(string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return append([]byte(nil), c.value...), nil
}

type runtimeGlobalCredentialInjector struct {
	value     []byte
	delivered string
}

func (i *runtimeGlobalCredentialInjector) InjectGlobalCredential(_ context.Context, _ string, value []byte) error {
	i.value = value
	i.delivered = string(value)
	return nil
}

type runtimeExternalCredentialAdapter struct {
	value []byte
	err   error
}

type failingGlobalCredentialUsageStore struct{ db.Store }

func (failingGlobalCredentialUsageStore) CreateGlobalCredentialUsage(db.GlobalCredentialUsage) (db.GlobalCredentialUsage, error) {
	return db.GlobalCredentialUsage{}, errors.New("usage storage unavailable")
}

func (a runtimeExternalCredentialAdapter) ResolveGlobalCredentialExternal(context.Context, db.GlobalCredentialExternalReference) (pro_interfaces.GlobalCredentialExternalResult, error) {
	if a.err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, a.err
	}
	return pro_interfaces.GlobalCredentialExternalResult{Value: append([]byte(nil), a.value...), ProviderVersion: 7}, nil
}

func TestGlobalCredentialRuntimeResolvesCurrentLocalVersionAndAuditsBeforeInjection(t *testing.T) {
	store, actor, project, credential, grant := runtimeGlobalCredentialFixture(t, db.GlobalCredentialMaterialLocalEncrypted)
	injector := &runtimeGlobalCredentialInjector{}
	resolver := NewGlobalCredentialRuntimeResolverWithDependencies(store, runtimeGlobalCredentialCipher{value: []byte("runtime-value")}, runtimeCapabilityProvider{active: true}, nil)

	snapshot, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
		TaskID: 41, ProjectID: project.ID, TemplateID: 99, ActorID: actor.ID,
		Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
	}, injector)

	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.GlobalCredentialResolutionAllowed, snapshot.Outcome)
	assert.Equal(t, grant.ID, snapshot.GrantID)
	assert.Equal(t, credential.CurrentVersion, snapshot.CredentialVersion)
	assert.Equal(t, "runtime-value", injector.delivered)
	for _, value := range injector.value {
		assert.Zero(t, value, "resolver must zero the transient material after injection")
	}
	usage, err := store.GetGlobalCredentialUsage(credential.ID, db.GlobalCredentialUsageQuery{Count: 10})
	require.NoError(t, err)
	require.Len(t, usage, 1)
	assert.Equal(t, "allowed", usage[0].Outcome)
	assert.NotContains(t, usage[0].Reason, "runtime-value")
}

func TestGlobalCredentialRuntimeFinalRecheckBlocksRevokedGrantWithoutInjection(t *testing.T) {
	store, actor, project, credential, grant := runtimeGlobalCredentialFixture(t, db.GlobalCredentialMaterialLocalEncrypted)
	resolver := NewGlobalCredentialRuntimeResolverWithDependencies(store, runtimeGlobalCredentialCipher{value: []byte("must-not-inject")}, runtimeCapabilityProvider{active: true}, nil).(*globalCredentialRuntimeResolver)
	resolver.beforeFinalCheck = func() {
		_, err := store.SetGlobalCredentialGrantStatus(credential.ID, grant.ID, db.GlobalCredentialGrantStatusRevoked, grant.Revision, &actor.ID, time.Now().UTC())
		require.NoError(t, err)
	}
	injector := &runtimeGlobalCredentialInjector{}

	snapshot, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
		TaskID: 42, ProjectID: project.ID, TemplateID: 99, ActorID: actor.ID,
		Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
	}, injector)

	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
	assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonGrantUnavailable, snapshot.Reason)
	assert.Empty(t, injector.value)
}

func TestGlobalCredentialRuntimeFailsClosedForProviderOutageAndCrossProjectBinding(t *testing.T) {
	store, actor, project, credential, _ := runtimeGlobalCredentialFixture(t, db.GlobalCredentialMaterialExternalReference)
	other, err := store.CreateProject(db.Project{Name: "other credential project"})
	require.NoError(t, err)
	resolver := NewGlobalCredentialRuntimeResolverWithDependencies(store, runtimeGlobalCredentialCipher{}, runtimeCapabilityProvider{active: true}, runtimeExternalCredentialAdapter{err: errors.New("provider raw failure")})
	injector := &runtimeGlobalCredentialInjector{}

	crossProject, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
		TaskID: 43, ProjectID: other.ID, TemplateID: 99, ActorID: actor.ID,
		Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
	}, injector)
	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
	assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonGrantUnavailable, crossProject.Reason)

	failure, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
		TaskID: 44, ProjectID: project.ID, TemplateID: 99, ActorID: actor.ID,
		Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
	}, injector)
	assert.EqualError(t, err, "external credential resolution failed")
	assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonProviderUnavailable, failure.Reason)
	assert.NotContains(t, failure.Reason, "raw")
}

func TestGlobalCredentialRuntimeUsesRotatedCurrentVersionAndBlocksWhenAuditCannotPersist(t *testing.T) {
	store, actor, project, credential, _ := runtimeGlobalCredentialFixture(t, db.GlobalCredentialMaterialLocalEncrypted)
	rotated, _, err := store.RotateGlobalCredential(credential.ID, credential.Revision, db.GlobalCredentialVersion{
		MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "sealed:rotated", CreatedByUserID: actor.ID,
	}, time.Now().UTC())
	require.NoError(t, err)
	resolver := NewGlobalCredentialRuntimeResolverWithDependencies(store, runtimeGlobalCredentialCipher{value: []byte("rotated")}, runtimeCapabilityProvider{active: true}, nil)

	snapshot, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
		TaskID: 45, ProjectID: project.ID, TemplateID: 99, ActorID: actor.ID,
		Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
	}, &runtimeGlobalCredentialInjector{})
	require.NoError(t, err)
	assert.Equal(t, rotated.CurrentVersion, snapshot.CredentialVersion)

	blocked := NewGlobalCredentialRuntimeResolverWithDependencies(failingGlobalCredentialUsageStore{Store: store}, runtimeGlobalCredentialCipher{value: []byte("must-not-inject")}, runtimeCapabilityProvider{active: true}, nil)
	injector := &runtimeGlobalCredentialInjector{}
	snapshot, err = blocked.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
		TaskID: 46, ProjectID: project.ID, TemplateID: 99, ActorID: actor.ID,
		Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
	}, injector)
	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialAuditUnavailable)
	assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonAuditUnavailable, snapshot.Reason)
	assert.Empty(t, injector.value)
}

func TestGlobalCredentialRuntimeAuditsActorlessTaskBeforeBlocking(t *testing.T) {
	store, _, project, credential, _ := runtimeGlobalCredentialFixture(t, db.GlobalCredentialMaterialLocalEncrypted)
	resolver := NewGlobalCredentialRuntimeResolverWithDependencies(store, runtimeGlobalCredentialCipher{value: []byte("must-not-inject")}, runtimeCapabilityProvider{active: true}, nil)
	injector := &runtimeGlobalCredentialInjector{}

	snapshot, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
		TaskID: 47, ProjectID: project.ID, TemplateID: 99,
		Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
	}, injector)
	assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
	assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonActorRequired, snapshot.Reason)
	assert.Empty(t, injector.value)
	usage, usageErr := store.GetGlobalCredentialUsage(credential.ID, db.GlobalCredentialUsageQuery{Count: 10})
	require.NoError(t, usageErr)
	require.Len(t, usage, 1)
	assert.Equal(t, 0, usage[0].ActorID)
	assert.Equal(t, "actor_required", usage[0].Reason)
}

func runtimeGlobalCredentialFixture(t *testing.T, kind db.GlobalCredentialMaterialKind) (*storepkg.SqlDb, db.User, db.Project, db.GlobalCredential, db.GlobalCredentialGrant) {
	t.Helper()
	store := storepkg.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	actor, err := store.CreateUserWithoutPassword(db.User{Username: "runtime-admin", Name: "Runtime Admin", Email: "runtime-admin@example.test", Admin: true})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "runtime credential project"})
	require.NoError(t, err)
	version := db.GlobalCredentialVersion{MaterialKind: kind, CreatedByUserID: actor.ID}
	if kind == db.GlobalCredentialMaterialLocalEncrypted {
		version.EncryptedMaterial = "sealed:runtime-value"
	} else {
		version.ExternalReference = db.GlobalCredentialExternalReference{Provider: "vault", ProviderID: "global", Mount: "kv", Path: "runtime/value", Version: 1, Field: "token"}
	}
	credential, _, err := store.CreateGlobalCredential(db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Runtime", OwnerUserID: actor.ID, Enabled: true, Created: time.Now().UTC()}, version)
	require.NoError(t, err)
	grant, err := store.CreateGlobalCredentialGrant(db.GlobalCredentialGrant{CredentialID: credential.ID, ProjectID: project.ID, Operations: db.GlobalCredentialGrantOperationConsume, Status: db.GlobalCredentialGrantStatusActive, Revision: 1, CreatedByUserID: actor.ID, Created: time.Now().UTC()})
	require.NoError(t, err)
	return store, actor, project, credential, grant
}

// principalStore answers task and principal lookups for an actorless task.
type principalStore struct {
	*storepkg.SqlDb
	principal int
}

func (s principalStore) GetTask(projectID int, taskID int) (db.Task, error) {
	scheduleID := 5
	return db.Task{ID: taskID, ProjectID: projectID, ScheduleID: &scheduleID}, nil
}

func (s principalStore) SetScheduleCredentialPrincipal(int, int) error    { return nil }
func (s principalStore) SetIntegrationCredentialPrincipal(int, int) error { return nil }
func (s principalStore) BeginCredentialPrincipalChange(db.CredentialPrincipalKind, int) (string, bool, error) {
	return "", false, nil
}
func (s principalStore) FinishCredentialPrincipalChange(db.CredentialPrincipalKind, int, string, int) error {
	return nil
}
func (s principalStore) AbortCredentialPrincipalChange(db.CredentialPrincipalKind, int, string) error {
	return nil
}
func (s principalStore) GetTaskCredentialPrincipal(db.Task) (int, bool, error) {
	return s.principal, s.principal > 0, nil
}

func TestGlobalCredentialRuntimeAuthorizesActorlessTasksThroughTheTriggerPrincipal(t *testing.T) {
	store, admin, project, credential, grant := runtimeGlobalCredentialFixture(t, db.GlobalCredentialMaterialLocalEncrypted)
	grant.Operations = db.GlobalCredentialGrantOperationConsume | db.GlobalCredentialGrantOperationConsumeUnattended
	_, err := store.UpdateGlobalCredentialGrant(grant, grant.Revision, time.Now().UTC())
	require.NoError(t, err)
	member, err := store.CreateUserWithoutPassword(db.User{Username: "runtime-member", Name: "Member", Email: "runtime-member@example.test"})
	require.NoError(t, err)
	resolve := func(principal int) (pro_interfaces.GlobalCredentialResolutionSnapshot, *runtimeGlobalCredentialInjector, error) {
		resolver := NewGlobalCredentialRuntimeResolverWithDependencies(principalStore{SqlDb: store, principal: principal},
			runtimeGlobalCredentialCipher{value: []byte("schedule-value")}, runtimeCapabilityProvider{active: true}, nil)
		injector := &runtimeGlobalCredentialInjector{}
		snapshot, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
			TaskID: 48, ProjectID: project.ID, TemplateID: 99,
			Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "TF_VAR_token", Env: true},
		}, injector)
		return snapshot, injector, err
	}

	t.Run("a principal allowed to consume receives the value", func(t *testing.T) {
		snapshot, injector, err := resolve(admin.ID)
		require.NoError(t, err)
		assert.Equal(t, pro_interfaces.GlobalCredentialResolutionAllowed, snapshot.Outcome)
		assert.Equal(t, admin.ID, snapshot.ActorID, "the usage history names the principal")
		assert.Equal(t, "schedule-value", injector.delivered)
	})

	t.Run("a principal without consume permission is denied", func(t *testing.T) {
		snapshot, injector, err := resolve(member.ID)
		assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
		assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonTaskPermission, snapshot.Reason)
		assert.Empty(t, injector.delivered)
	})

	t.Run("a disabled principal is denied", func(t *testing.T) {
		disabled, err := store.CreateUserWithoutPassword(db.User{Username: "runtime-disabled", Name: "Disabled", Email: "runtime-disabled@example.test", Admin: true})
		require.NoError(t, err)
		require.NoError(t, store.SetUserDisabledAndRevokeCredentials(disabled.ID, true))
		snapshot, injector, err := resolve(disabled.ID)
		assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
		assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonActorRequired, snapshot.Reason)
		assert.Empty(t, injector.delivered)
	})

	t.Run("a trigger without principal is denied", func(t *testing.T) {
		snapshot, injector, err := resolve(0)
		assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
		assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonActorRequired, snapshot.Reason)
		assert.Empty(t, injector.delivered)
	})

	t.Run("revoking unattended use blocks an authorized principal", func(t *testing.T) {
		current, err := store.GetGlobalCredentialGrantForProject(credential.ID, project.ID)
		require.NoError(t, err)
		current.Operations = db.GlobalCredentialGrantOperationConsume
		_, err = store.UpdateGlobalCredentialGrant(current, current.Revision, time.Now().UTC())
		require.NoError(t, err)
		snapshot, injector, err := resolve(admin.ID)
		assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
		assert.Equal(t, pro_interfaces.GlobalCredentialResolutionReasonActorRequired, snapshot.Reason)
		assert.Empty(t, injector.delivered)
	})

	t.Run("a started task is checked against its user, not a principal", func(t *testing.T) {
		resolver := NewGlobalCredentialRuntimeResolverWithDependencies(principalStore{SqlDb: store, principal: admin.ID},
			runtimeGlobalCredentialCipher{value: []byte("schedule-value")}, runtimeCapabilityProvider{active: true}, nil)
		injector := &runtimeGlobalCredentialInjector{}
		snapshot, err := resolver.ResolveAndInject(context.Background(), pro_interfaces.GlobalCredentialResolutionRequest{
			TaskID: 49, ProjectID: project.ID, TemplateID: 99, ActorID: member.ID,
			Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credential.ID, Target: "deploy_token"},
		}, injector)
		assert.ErrorIs(t, err, pro_interfaces.ErrGlobalCredentialResolutionDenied)
		assert.Equal(t, member.ID, snapshot.ActorID)
		assert.Empty(t, injector.delivered)
	})
}

func TestGlobalCredentialTaskBindingTargets(t *testing.T) {
	tests := []struct {
		name    string
		binding pro_interfaces.GlobalCredentialTaskBinding
		valid   bool
	}{
		{"extra variable", pro_interfaces.GlobalCredentialTaskBinding{CredentialID: 1, Target: "deploy_token"}, true},
		{"environment variable", pro_interfaces.GlobalCredentialTaskBinding{CredentialID: 1, Target: "TF_VAR_token", Env: true}, true},
		{"environment name as extra variable", pro_interfaces.GlobalCredentialTaskBinding{CredentialID: 1, Target: "TF_VAR_token"}, false},
		{"reserved environment variable", pro_interfaces.GlobalCredentialTaskBinding{CredentialID: 1, Target: "SEMAPHORE_JWT", Env: true}, false},
		{"missing credential", pro_interfaces.GlobalCredentialTaskBinding{Target: "deploy_token"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.valid {
				assert.NoError(t, tt.binding.Validate())
			} else {
				assert.Error(t, tt.binding.Validate())
			}
		})
	}
}
