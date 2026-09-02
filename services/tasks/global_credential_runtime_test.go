package tasks

import (
	"context"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskCredentialResolverStub struct{ targets []string }

func (s *taskCredentialResolverStub) ResolveAndInject(ctx context.Context, request pro_interfaces.GlobalCredentialResolutionRequest, injector pro_interfaces.GlobalCredentialInjector) (pro_interfaces.GlobalCredentialResolutionSnapshot, error) {
	s.targets = append(s.targets, request.Binding.Target)
	if err := injector.InjectGlobalCredential(ctx, request.Binding.Target, []byte("resolved-"+request.Binding.Target)); err != nil {
		return pro_interfaces.GlobalCredentialResolutionSnapshot{}, err
	}
	return pro_interfaces.GlobalCredentialResolutionSnapshot{Outcome: pro_interfaces.GlobalCredentialResolutionAllowed}, nil
}

func TestResolveTaskGlobalCredentialsMergesValueOnlyAtDispatchInStableOrder(t *testing.T) {
	resolver := &taskCredentialResolverStub{}
	pool := &TaskPool{globalCredentialResolver: resolver, logger: make(chan logRecord, 1)}
	runner := &TaskRunner{Task: db.Task{
		ID: 3, ProjectID: 4, TemplateID: 5,
		GlobalCredentialBindings: map[string]int{"z_token": 8, "a_token": 7},
	}}

	merged, err := pool.ResolveTaskGlobalCredentials(context.Background(), runner, nil, `{"survey":"retained"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"a_token", "z_token"}, resolver.targets)
	assert.JSONEq(t, `{"survey":"retained","a_token":"resolved-a_token","z_token":"resolved-z_token"}`, merged)
	assert.NotContains(t, runner.Task.GlobalCredentialBindingsJSON, "resolved-")
}

func TestResolveTaskGlobalCredentialsFailsClosedWithoutResolver(t *testing.T) {
	pool := &TaskPool{}
	runner := &TaskRunner{Task: db.Task{GlobalCredentialBindings: map[string]int{"token": 1}}}
	_, err := pool.ResolveTaskGlobalCredentials(context.Background(), runner, nil, "")
	assert.Error(t, err)
}

func TestResolveTaskGlobalCredentialsDecodesPersistedBindingsBeforeDispatch(t *testing.T) {
	resolver := &taskCredentialResolverStub{}
	pool := &TaskPool{globalCredentialResolver: resolver, logger: make(chan logRecord, 1)}
	runner := &TaskRunner{Task: db.Task{
		ID: 3, ProjectID: 4, TemplateID: 5,
		GlobalCredentialBindingsJSON: `{"deploy_token":7}`,
	}, pool: pool}

	merged, err := pool.ResolveTaskGlobalCredentials(context.Background(), runner, nil, `{"survey":"retained"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"deploy_token"}, resolver.targets)
	assert.Equal(t, []string{"deploy_token"}, runner.GlobalCredentialBindingTargets())
	assert.JSONEq(t, `{"survey":"retained","deploy_token":"resolved-deploy_token"}`, merged)

	runner.SetTaskCredentialRedaction(merged)
	runner.Log("credential=resolved-deploy_token")
	record := <-pool.logger
	assert.NotContains(t, record.output, "resolved-deploy_token")
}

func TestResolveTaskGlobalCredentialsRejectsSurveyTargetCollision(t *testing.T) {
	resolver := &taskCredentialResolverStub{}
	pool := &TaskPool{globalCredentialResolver: resolver}
	runner := &TaskRunner{Task: db.Task{
		ID: 3, ProjectID: 4, TemplateID: 5,
		GlobalCredentialBindings: map[string]int{"deploy_token": 7},
	}}

	_, err := pool.ResolveTaskGlobalCredentials(context.Background(), runner, nil, `{"deploy_token":"survey-value"}`)
	assert.Error(t, err)
}

func TestTaskRunnerRedactsResolvedCredentialBeforeServerLogSink(t *testing.T) {
	pool := &TaskPool{logger: make(chan logRecord, 1)}
	runner := &TaskRunner{
		Task: db.Task{ID: 3, ProjectID: 4, GlobalCredentialBindings: map[string]int{"deploy_token": 7}},
		pool: pool,
	}
	runner.SetTaskCredentialRedaction(`{"deploy_token":"server-secret-value"}`)
	runner.Log("credential=server-secret-value")

	record := <-pool.logger
	assert.NotContains(t, record.output, "server-secret-value")
	assert.Contains(t, record.output, "[REDACTED]")
}

func TestBlockedTaskStatusIsTerminalAndFailsRunnerAttempt(t *testing.T) {
	assert.False(t, taskStatusTransitionAllowed(task_logger.TaskBlockedStatus, task_logger.TaskRunningStatus))
	assert.Equal(t, db.RunnerAttemptFailed, runnerAttemptOutcomeForStatus(task_logger.TaskBlockedStatus))
}
