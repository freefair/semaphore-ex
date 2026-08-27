package cmd

import (
	"os"
	"time"

	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type debugFilterSource struct {
	flagValue    string
	flagSet      bool
	configPath   string
	fallbackSpec string
	lookupEnv    func(string) (string, bool)
}

func newDebugFilterSource(configPath *string) debugFilterSource {
	path := ""
	if configPath != nil {
		path = *configPath
	}
	fallback := ""
	if util.Config != nil && util.Config.Log != nil {
		fallback = util.Config.Log.DebugFilter
	}
	return debugFilterSource{
		flagValue:    persistentFlags.debugFilter,
		flagSet:      rootCmd.PersistentFlags().Changed("debug-filter"),
		configPath:   path,
		fallbackSpec: fallback,
		lookupEnv:    os.LookupEnv,
	}
}

func (s debugFilterSource) Load() (string, error) {
	if s.flagSet {
		return s.flagValue, nil
	}
	lookupEnv := s.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if value, ok := lookupEnv("SEMAPHORE_DEBUG_FILTER"); ok {
		return value, nil
	}
	if s.configPath != "" {
		return util.ReadDebugFilterConfig(s.configPath)
	}
	return s.fallbackSpec, nil
}

func reloadDebugFilter(
	manager *debuglog.Manager,
	source debugFilterSource,
	loadedAt time.Time,
) pro_interfaces.DebugFilterDiagnostics {
	spec, err := source.Load()
	if err != nil {
		return manager.RecordReloadError(err.Error(), loadedAt)
	}
	return manager.Reload(spec, loadedAt)
}

func debugLogInstance() string {
	if util.Config != nil && util.Config.HA != nil && util.Config.HA.NodeID != "" {
		return util.Config.HA.NodeID
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}
	return "semaphore"
}
