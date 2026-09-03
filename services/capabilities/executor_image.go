package capabilities

import (
	"github.com/semaphoreui/semaphore/db"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
)

// ExecutorImageResolver resolves the user- and subscription-specific container executor feature.
type ExecutorImageResolver func(*db.User) bool

// NewExecutorImageResolver keeps template writes and every task-start path on the same decision.
func NewExecutorImageResolver() ExecutorImageResolver {
	return func(user *db.User) bool {
		features := proFeatures.GetFeatures()
		return features.DockerExecutor || features.K8sExecutor
	}
}
