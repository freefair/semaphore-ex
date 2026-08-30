package k8s

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"k8s.io/apimachinery/pkg/util/validation"
)

const maxPullSecrets = 16

type config struct {
	kubeconfigPath        string
	context               string
	clusterAlias          string
	namespace             string
	image                 string
	helperImage           string
	serviceAccount        string
	pullSecrets           []string
	pollInterval          time.Duration
	cleanupGrace          time.Duration
	activeDeadlineSeconds int64
}

func effectiveConfig(input util.RunnerK8sConfig) (config, error) {
	result := config{
		kubeconfigPath:        strings.TrimSpace(input.KubeconfigPath),
		context:               strings.TrimSpace(input.Context),
		clusterAlias:          strings.TrimSpace(input.ClusterAlias),
		namespace:             strings.TrimSpace(input.Namespace),
		image:                 strings.TrimSpace(input.Image),
		helperImage:           strings.TrimSpace(input.HelperImage),
		serviceAccount:        strings.TrimSpace(input.ServiceAccount),
		pollInterval:          time.Duration(input.PollIntervalSeconds) * time.Second,
		cleanupGrace:          time.Duration(input.CleanupGraceSeconds) * time.Second,
		activeDeadlineSeconds: int64(input.ActiveDeadlineSeconds),
	}
	if result.namespace == "" {
		result.namespace = "semaphore"
	}
	if result.pollInterval == 0 {
		result.pollInterval = 3 * time.Second
	}
	if result.cleanupGrace == 0 {
		result.cleanupGrace = 30 * time.Second
	}
	if result.activeDeadlineSeconds == 0 {
		result.activeDeadlineSeconds = 3600
	}

	if result.kubeconfigPath != "" {
		if !filepath.IsAbs(result.kubeconfigPath) {
			return config{}, fmt.Errorf("Kubernetes kubeconfig path must be absolute")
		}
		if result.context == "" {
			return config{}, fmt.Errorf("Kubernetes kubeconfig requires an explicit context")
		}
	} else if result.context != "" {
		return config{}, fmt.Errorf("Kubernetes context requires a kubeconfig path")
	}
	if result.clusterAlias == "" || len(result.clusterAlias) > 128 {
		return config{}, fmt.Errorf("Kubernetes cluster alias must contain 1 to 128 bytes")
	}
	if errors := validation.IsDNS1123Label(result.namespace); len(errors) > 0 {
		return config{}, fmt.Errorf("invalid Kubernetes namespace: %s", strings.Join(errors, "; "))
	}
	if result.serviceAccount == "" {
		return config{}, fmt.Errorf("Kubernetes service account is required")
	}
	if result.serviceAccount == "default" {
		return config{}, fmt.Errorf("Kubernetes default service account is not allowed for task workloads")
	}
	if errors := validation.IsDNS1123Subdomain(result.serviceAccount); len(errors) > 0 {
		return config{}, fmt.Errorf("invalid Kubernetes service account: %s", strings.Join(errors, "; "))
	}
	if _, err := immutableImage(result.image, "task image"); err != nil {
		return config{}, err
	}
	if _, err := immutableImage(result.helperImage, "helper image"); err != nil {
		return config{}, err
	}
	if result.pollInterval < time.Second || result.pollInterval > time.Minute {
		return config{}, fmt.Errorf("Kubernetes poll interval must be between 1 and 60 seconds")
	}
	if result.cleanupGrace < time.Second || result.cleanupGrace > 5*time.Minute {
		return config{}, fmt.Errorf("Kubernetes cleanup grace must be between 1 and 300 seconds")
	}
	if result.activeDeadlineSeconds < 1 || result.activeDeadlineSeconds > 24*60*60 {
		return config{}, fmt.Errorf("Kubernetes active deadline must be between 1 and 86400 seconds")
	}

	seen := map[string]struct{}{}
	for _, raw := range strings.Split(input.PullSecrets, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if errors := validation.IsDNS1123Subdomain(name); len(errors) > 0 {
			return config{}, fmt.Errorf("invalid Kubernetes image pull secret: %s", strings.Join(errors, "; "))
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		result.pullSecrets = append(result.pullSecrets, name)
		if len(result.pullSecrets) > maxPullSecrets {
			return config{}, fmt.Errorf("Kubernetes image pull secrets must contain at most %d entries", maxPullSecrets)
		}
	}
	return result, nil
}

func immutableImage(value string, field string) (string, error) {
	normalized, err := db.NormalizeExecutorImage(value)
	if err != nil || normalized == nil {
		return "", fmt.Errorf("Kubernetes %s must be a valid immutable OCI image", field)
	}
	image := *normalized
	digestIndex := strings.LastIndex(image, "@sha256:")
	if digestIndex <= 0 || len(image)-digestIndex != len("@sha256:")+64 {
		return "", fmt.Errorf("Kubernetes %s must use an immutable sha256 digest", field)
	}
	for _, character := range image[digestIndex+len("@sha256:"):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return "", fmt.Errorf("Kubernetes %s must use an immutable sha256 digest", field)
			}
		}
	}
	return image, nil
}

func (c config) taskImage(template db.Template) (string, error) {
	if template.ExecutorImage == nil {
		return immutableImage(c.image, "default task image")
	}
	return immutableImage(*template.ExecutorImage, "task image")
}
