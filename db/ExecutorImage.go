package db

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrExecutorImageCapabilityUnavailable = errors.New("executor image capability is unavailable")
	ErrExecutorImageIncompatible          = errors.New("no matching runner supports executor image overrides")
	ErrExecutorImageInvalid               = errors.New("executor image is invalid")
)

const MaxExecutorImageLength = 255

var executorImagePattern = regexp.MustCompile(
	`^(?:[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]+)?/)?` +
		`[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*` +
		`(?:/[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*)*` +
		`(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?` +
		`(?:@[A-Za-z][A-Za-z0-9]*(?:[+._-][A-Za-z0-9]+)*:[A-Fa-f0-9]{32,})?$`,
)

// NormalizeExecutorImage trims and validates a credential-free OCI/Docker image reference.
// A nil result means the runner's configured default image should be used.
func NormalizeExecutorImage(value string) (*string, error) {
	image := strings.TrimSpace(value)
	if image == "" {
		return nil, nil
	}
	if len(image) > MaxExecutorImageLength {
		return nil, fmt.Errorf("%w: must contain at most %d bytes", ErrExecutorImageInvalid, MaxExecutorImageLength)
	}
	if strings.Contains(image, "://") || !executorImagePattern.MatchString(image) {
		return nil, fmt.Errorf("%w: must be a credential-free OCI image reference", ErrExecutorImageInvalid)
	}
	return &image, nil
}
