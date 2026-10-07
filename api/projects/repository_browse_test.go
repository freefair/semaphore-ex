package projects

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryController_lockBrowseDir_SerializesSameDir(t *testing.T) {
	c := &RepositoryController{}

	unlock, err := c.lockBrowseDir(context.Background(), "repository_1_browse_aaaa")
	require.NoError(t, err)

	acquired := make(chan struct{})
	go func() {
		second, lockErr := c.lockBrowseDir(context.Background(), "repository_1_browse_aaaa")
		if lockErr != nil {
			t.Error(lockErr)
			return
		}
		close(acquired)
		second()
	}()

	select {
	case <-acquired:
		t.Fatal("second request entered the scratch checkout while the first still held it")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second request was not let in after the first released the checkout")
	}
}

func TestRepositoryController_lockBrowseDir_IndependentDirs(t *testing.T) {
	c := &RepositoryController{}

	unlockA, err := c.lockBrowseDir(context.Background(), "repository_1_browse_aaaa")
	require.NoError(t, err)
	defer unlockA()

	acquired := make(chan struct{})
	go func() {
		unlockB, lockErr := c.lockBrowseDir(context.Background(), "repository_1_browse_bbbb")
		if lockErr != nil {
			t.Error(lockErr)
			return
		}
		close(acquired)
		unlockB()
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("a different scratch checkout was blocked by an unrelated lock")
	}
}

func TestRepositoryController_lockBrowseDir_Reentrant(t *testing.T) {
	c := &RepositoryController{}

	var wg sync.WaitGroup
	counter := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, lockErr := c.lockBrowseDir(context.Background(), "same")
			if lockErr != nil {
				t.Error(lockErr)
				return
			}
			defer unlock()
			counter++
		}()
	}
	wg.Wait()

	assert.Equal(t, 20, counter)
}

func TestRepositoryController_lockBrowseDir_StopsWaitingWhenRequestCancelled(t *testing.T) {
	c := &RepositoryController{}
	unlock, err := c.lockBrowseDir(context.Background(), "repository_1_browse_aaaa")
	require.NoError(t, err)
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	second, err := c.lockBrowseDir(ctx, "repository_1_browse_aaaa")
	assert.Nil(t, second)
	assert.ErrorIs(t, err, context.Canceled)
}
