package sql

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/require"
)

func TestRegisterRunnerAtomicallyConsumesToken(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "atomic registration"})
	require.NoError(t, err)
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	expiresAt := time.Now().Add(time.Hour)
	runner, err := store.CreateRunner(db.Runner{
		Name:                       "project runner",
		ProjectID:                  &project.ID,
		RegistrationTokenHash:      &hash,
		RegistrationTokenExpiresAt: &expiresAt,
	})
	require.NoError(t, err)

	const contenders = 64
	start := make(chan struct{})
	var successes atomic.Int32
	var unexpectedRunnerIDs atomic.Int32
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			registered, registerErr := store.RegisterRunner(hash, nil)
			if registerErr == nil {
				if registered.ID != runner.ID {
					unexpectedRunnerIDs.Add(1)
				}
				successes.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()

	require.Equal(t, int32(1), successes.Load())
	require.Zero(t, unexpectedRunnerIDs.Load())
}
