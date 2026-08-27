package runners

import (
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestValidateExecutorImageCompatibility(t *testing.T) {
	image := "registry.example.com/team/job:v1"
	for _, executorType := range []util.ExecutorType{util.ExecutorTypeDocker, util.ExecutorTypeKubernetes} {
		assert.NoError(t, validateExecutorImageCompatibility(&image, executorType))
	}
	require.ErrorContains(t, validateExecutorImageCompatibility(&image, util.ExecutorTypeLocal), "Docker or Kubernetes")
	assert.NoError(t, validateExecutorImageCompatibility(nil, util.ExecutorTypeLocal))
}
