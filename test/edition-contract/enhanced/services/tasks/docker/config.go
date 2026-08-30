// Package docker implements the enhanced-edition Docker task executor.
package docker

import (
	"fmt"
	"strings"
	"time"

	"github.com/docker/go-units"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

type PullPolicy string

const (
	PullAlways       PullPolicy = "always"
	PullIfNotPresent PullPolicy = "if-not-present"
	PullNever        PullPolicy = "never"
)

type config struct {
	host         string
	tlsVerify    bool
	certPath     string
	image        string
	helperImage  string
	network      string
	pullPolicy   PullPolicy
	nanoCPUs     int64
	memory       int64
	pollInterval time.Duration
	cleanupGrace time.Duration
	privileged   bool
}

func effectiveConfig(input util.RunnerDockerConfig) (config, error) {
	result := config{
		host:         strings.TrimSpace(input.Host),
		tlsVerify:    input.TLSVerify,
		certPath:     strings.TrimSpace(input.CertPath),
		image:        strings.TrimSpace(input.Image),
		helperImage:  strings.TrimSpace(input.HelperImage),
		network:      strings.TrimSpace(input.Network),
		pullPolicy:   PullPolicy(strings.TrimSpace(input.PullPolicy)),
		cleanupGrace: time.Duration(input.CleanupGraceSeconds) * time.Second,
		pollInterval: time.Duration(input.PollIntervalSeconds) * time.Second,
		privileged:   input.Privileged,
	}
	if result.image == "" {
		result.image = "semaphoreui/job:latest"
	}
	if result.helperImage == "" {
		result.helperImage = "semaphoreui/helper:latest"
	}
	if result.network == "" {
		result.network = "none"
	}
	if result.pullPolicy == "" {
		result.pullPolicy = PullIfNotPresent
	}
	if result.pollInterval == 0 {
		result.pollInterval = 2 * time.Second
	}
	if result.cleanupGrace == 0 {
		result.cleanupGrace = 30 * time.Second
	}

	if _, err := requiredImage(result.image, "default image"); err != nil {
		return config{}, err
	}
	if _, err := requiredImage(result.helperImage, "helper image"); err != nil {
		return config{}, err
	}
	switch result.pullPolicy {
	case PullAlways, PullIfNotPresent, PullNever:
	default:
		return config{}, fmt.Errorf("unsupported Docker pull policy %q", input.PullPolicy)
	}
	if input.CPULimit < 0 {
		return config{}, fmt.Errorf("Docker CPU limit must not be negative")
	}
	if input.Privileged {
		return config{}, fmt.Errorf("Docker privileged execution is not supported")
	}
	result.nanoCPUs = int64(input.CPULimit * 1_000_000_000)
	if input.MemoryLimit != "" {
		memory, err := units.RAMInBytes(input.MemoryLimit)
		if err != nil || memory <= 0 {
			return config{}, fmt.Errorf("invalid Docker memory limit %q", input.MemoryLimit)
		}
		result.memory = memory
	}
	if input.PollIntervalSeconds < 0 {
		return config{}, fmt.Errorf("Docker poll interval must not be negative")
	}
	if input.CleanupGraceSeconds < 0 {
		return config{}, fmt.Errorf("Docker cleanup grace must not be negative")
	}
	if result.certPath != "" && !result.tlsVerify {
		return config{}, fmt.Errorf("Docker cert path requires TLS verification")
	}
	return result, nil
}

func requiredImage(value string, field string) (string, error) {
	image, err := db.NormalizeExecutorImage(value)
	if err != nil {
		return "", fmt.Errorf("invalid Docker %s: %w", field, err)
	}
	if image == nil {
		return "", fmt.Errorf("Docker %s must not be empty", field)
	}
	return *image, nil
}

func (c config) taskImage(template db.Template) (string, error) {
	if template.ExecutorImage == nil {
		return requiredImage(c.image, "default image")
	}
	return requiredImage(*template.ExecutorImage, "task image")
}
