package util

import (
	"gopkg.in/natefinch/lumberjack.v2"
)

type DebugLogType struct {
	Enabled bool               `json:"enabled" env:"SEMAPHORE_DEBUG_LOG_ENABLED"`
	Format  string             `json:"format,omitempty" env:"SEMAPHORE_DEBUG_LOG_FORMAT"`
	Logger  *lumberjack.Logger `json:"logger,omitempty" env:"SEMAPHORE_DEBUG_LOGGER"`
}
