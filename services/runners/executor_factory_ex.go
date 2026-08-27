package runners

import (
	"fmt"
	"github.com/semaphoreui/semaphore/util"
)

func validateExecutorImageCompatibility(image *string, executorType util.ExecutorType) error {
	if image == nil {
		return nil
	}
	if executorType != util.ExecutorTypeDocker && executorType != util.ExecutorTypeKubernetes {
		return fmt.Errorf("executor image override requires a Docker or Kubernetes runner")
	}
	return nil
}
