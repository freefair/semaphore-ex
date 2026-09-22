package tasks

import (
	"context"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

type delayedStopApp struct {
	db_lib.LocalApp
	started, canceled, release chan struct{}
}

func (a *delayedStopApp) Run(args db_lib.LocalAppRunningArgs) error {
	close(a.started)
	<-args.StopCh
	close(a.canceled)
	<-a.release
	return nil
}
func (*delayedStopApp) Clear() {}
func TestLocalExecutorConfirmedStopWaitsForRunExit(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		name := "terraform"
		if grouped {
			name = "grouped shell"
		}
		t.Run(name, func(t *testing.T) {
			app := &delayedStopApp{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
			e := &LocalExecutor{App: app, prepared: true, Template: db.Template{App: db.AppTerraform}}
			if grouped {
				e.Template.App = db.AppBash
				e.Task.TaskGroupKeys = []string{"group/1"}
			}
			finished := make(chan struct{})
			var runErr error
			go func() { defer close(finished); runErr = e.Run("", nil, "") }()
			var release sync.Once
			t.Cleanup(func() { e.Kill(); release.Do(func() { close(app.release) }); <-finished })
			<-app.started
			stopper, ok := any(e).(ConfirmedStopper)
			require.True(t, ok, "runner must wait for local process exit before reporting stopped")
			require.Equal(t, StopPending, stopper.ConfirmStop(context.Background()))
			<-app.canceled
			require.Equal(t, StopPending, stopper.ConfirmStop(context.Background()))
			release.Do(func() { close(app.release) })
			<-finished
			require.NoError(t, runErr)
			require.Equal(t, StopConfirmed, stopper.ConfirmStop(context.Background()))
		})
	}
}
