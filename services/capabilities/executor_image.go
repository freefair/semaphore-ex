package capabilities

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// ExecutorImageResolver resolves the user- and subscription-specific container executor feature.
type ExecutorImageResolver func(*db.User) bool

// NewExecutorImageResolver keeps template writes and every task-start path on the same decision.
func NewExecutorImageResolver(subscriptionService pro_interfaces.SubscriptionService) ExecutorImageResolver {
	return func(user *db.User) bool {
		plan := ""
		if subscriptionService != nil {
			token, err := subscriptionService.GetToken()
			if err == nil && token.State != "expired" {
				plan = token.Plan
			} else if err != nil && !errors.Is(err, db.ErrNotFound) {
				plan = ""
			}
		}
		features := proFeatures.GetFeatures(user, plan)
		return features.DockerExecutor || features.K8sExecutor
	}
}
