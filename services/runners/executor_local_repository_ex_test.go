package runners

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHydrateLegacyLocalRepositoryPolicy(t *testing.T) {
	job := JobData{Repository: db.Repository{GitURL: "/srv/local", Monorepo: true}}
	require.NoError(t, hydrateJobAccessKeys(&job, map[int]db.AccessKey{}))
	assert.False(t, job.Repository.Monorepo)
}
