package cmd

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/factory"
	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// watchRuntimeConfigurationReload enables runtime configuration changes:
//   - a SIGHUP forces an immediate reload;
//   - a background poller applies changes to the encryption-keys file (and the
//     key files it references) automatically. The poller runs only when a keys
//     file is configured and the poll interval is positive.
func watchRuntimeConfigurationReload(debugFilter *debuglog.Manager, source debugFilterSource) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	go func() {
		for range sigCh {
			if err := util.ReloadEncryptionKeys(); err != nil {
				log.WithError(err).Error("failed to reload encryption keys")
			} else {
				log.Info("encryption keys reloaded (SIGHUP)")
			}

			diagnostics := reloadDebugFilter(debugFilter, source, time.Now().UTC())
			entry := log.WithFields(log.Fields{
				"debug_filter_effective": diagnostics.Effective,
				"debug_filter_rejected":  len(diagnostics.Rejected),
			})
			if diagnostics.ReloadError != "" {
				entry.WithField("debug_filter_reload_error", diagnostics.ReloadError).
					Error("failed to reload debug filter; retaining last-known-good filter")
			} else if len(diagnostics.Rejected) > 0 {
				entry.Warn("debug filter reloaded with rejected entries")
			} else {
				entry.Info("debug filter reloaded (SIGHUP)")
			}
		}
	}()

	interval := util.Config.EncryptionKeysPollInterval()
	if util.Config.EncryptionKeysFile() == "" || interval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			changed, err := util.ReloadEncryptionKeysIfChanged()
			if err != nil {
				log.WithError(err).Error("failed to reload encryption keys")
			} else if changed {
				log.Info("encryption keys reloaded (file changed)")
			}
		}
	}()
}

func createStoreWithMigrationVersionAndConfigPath(token string, undoTo *string, applyTo *string) (db.Store, *string) {
	usedConfigPath := util.ConfigInit(persistentFlags.configPath, persistentFlags.noConfig)

	store := factory.CreateStore()

	store.Connect()

	var err error
	if undoTo != nil {
		err = db.Rollback(store, *undoTo)
	} else {
		err = db.Migrate(store, applyTo)
	}

	if err != nil {
		panic(err)
	}

	err = db.FillConfigFromDB(store)

	if err != nil {
		panic(err)
	}

	util.LookupDefaultApps()

	return store, usedConfigPath
}
