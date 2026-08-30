package cmd

import (
	"context"
	"fmt"
	"github.com/gorilla/handlers"
	"github.com/semaphoreui/semaphore/api"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	proHA "github.com/semaphoreui/semaphore/pro/services/ha"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	proTasks "github.com/semaphoreui/semaphore/pro/services/tasks"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	identityServices "github.com/semaphoreui/semaphore/services/identity"
	"github.com/semaphoreui/semaphore/services/schedules"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var persistentFlags struct {
	configPath  string
	noConfig    bool
	logLevel    string
	debugFilter string
}

var rootCmd = &cobra.Command{
	Use:   "semaphore",
	Short: "Semaphore UI is a beautiful web UI for Ansible",
	Long: `Semaphore UI is a beautiful web UI for Ansible.
Source code is available at https://github.com/semaphoreui/semaphore.
Complete documentation is available at https://semaphoreui.com.`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
		os.Exit(0)
	},

	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		str := persistentFlags.logLevel
		if str == "" {
			str = os.Getenv("SEMAPHORE_LOG_LEVEL")
		}

		if str != "" {
			lvl, err := log.ParseLevel(str)
			if err != nil {
				log.Panic(err)
			}

			fmt.Println("Log level set to", lvl)
			log.SetLevel(lvl)
		}

	},
}

func Execute() {
	rootCmd.PersistentFlags().StringVar(&persistentFlags.logLevel, "log-level", "", "Log level: DEBUG, INFO, WARN, ERROR, FATAL, PANIC")
	rootCmd.PersistentFlags().StringVar(&persistentFlags.debugFilter, "debug-filter", "", "Debug component filter, e.g. 'runner,task_*' or '*,-db'")
	rootCmd.PersistentFlags().StringVar(&persistentFlags.configPath, "config", "", "Configuration file path")
	rootCmd.PersistentFlags().BoolVar(&persistentFlags.noConfig, "no-config", false, "Don't use configuration file")
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runService() {
	store, configPath := createStoreWithMigrationVersionAndConfigPath("root", nil, nil)

	filterSource := newDebugFilterSource(configPath)
	debugFilterSpec, debugFilterErr := filterSource.Load()
	loadedAt := time.Now().UTC()
	var debugFilter *debuglog.Manager
	if debugFilterErr != nil {
		debugFilter = debuglog.NewManagerWithReloadError(debugLogInstance(), debugFilterErr.Error(), loadedAt)
	} else {
		debugFilter = debuglog.NewManager(debugLogInstance(), debugFilterSpec, loadedAt)
	}
	log.SetFormatter(debuglog.NewFilteringFormatter(log.StandardLogger().Formatter, debugFilter))
	watchRuntimeConfigurationReload(debugFilter, filterSource)

	jwtSigner, jwtErr := util.InitJWTSignerFromStore(store)
	if jwtErr != nil {
		log.WithError(jwtErr).Warning("failed to initialise JWT signer")
	}

	initSyslog(util.Config.Syslog, debugFilter)

	state := proTasks.NewTaskStateStore()
	terraformStore := proFactory.NewTerraformStore(store)
	ansibleTaskRepo := proFactory.NewAnsibleTaskRepository(store)
	workflowStore := proFactory.NewWorkflowStore(store)
	workflowTriggerStore := proFactory.NewWorkflowTriggerStore(store)

	projectService := server.NewProjectService(store, store)
	capabilityProvider := proFeatures.NewCapabilityProvider(store)
	encryptionService := server.NewAccessKeyEncryptionService(store, store, store, store, capabilityProvider)
	accessKeyInstallationService := server.NewAccessKeyInstallationService(encryptionService)
	integrationService := server.NewIntegrationService(store, encryptionService)
	inventoryService := server.NewInventoryService(
		store,
		store,
		store,
		encryptionService,
	)
	accessKeyService := server.NewAccessKeyService(store, encryptionService, store, capabilityProvider)
	secretStorageService := server.NewSecretStorageService(store, store, accessKeyService, encryptionService)
	secretStorageSyncScheduler := server.NewSecretStorageSyncScheduler(store, secretStorageService)
	ldapGroupService := proFeatures.NewLDAPService(store, capabilityProvider, identityServices.NewLDAPClient())
	if err := ldapGroupService.Initialize(context.Background()); err != nil {
		log.WithError(err).Panic("failed to initialize LDAP group reconciliation service")
	}
	ldapGroupScheduler := server.NewLDAPGroupReconciliationScheduler(ldapGroupService)
	environmentService := server.NewEnvironmentService(store, encryptionService, store)
	runnerService := server.NewRunnerService(store)
	subscriptionService := proServer.NewSubscriptionService(store, store, store, terraformStore)
	logWriteService := proServer.NewLogWriteServiceWithFilter(debugFilter)
	defer func() {
		if err := logWriteService.Close(); err != nil {
			log.WithError(err).Error("failed to flush structured logs during shutdown")
		}
	}()
	appMetrics := metrics.NewMetrics()
	auditWebhookService := proServer.NewAuditWebhookService(store, appMetrics)
	auditWebhookService.Start()
	defer func() {
		if err := auditWebhookService.Close(); err != nil {
			log.WithError(err).Error("failed to stop audit webhook service")
		}
	}()

	taskPool := tasks.CreateTaskPool(
		store,
		state,
		ansibleTaskRepo,
		inventoryService,
		encryptionService,
		accessKeyInstallationService,
		logWriteService,
		jwtSigner,
		appMetrics,
	)

	// The workflow service orchestrates workflow runs and launches each node's
	// task through the pool; the pool calls back into it when a workflow task
	// finishes. Wire the cycle: pool first, then service (with the pool as its
	// enqueuer), then inject the service back into the pool. The run locker is
	// SQL-backed in HA mode (cluster-wide progression ownership) and nil
	// otherwise, which makes the service fall back to its in-process locker.
	workflowRunLocker := proHA.NewWorkflowRunLocker(store)
	workflowService := proServer.NewWorkflowService(workflowStore, store, &taskPool, workflowRunLocker, encryptionService)
	workflowDefinitionService := proServer.NewWorkflowDefinitionService(workflowStore, store)
	workflowTriggerService := proServer.NewWorkflowTriggerService(
		workflowTriggerStore, workflowStore, workflowService, store, capabilityProvider,
	)
	taskPool.SetWorkflowService(workflowService)
	workflowTriggerScheduler := proServer.NewWorkflowTriggerScheduler(workflowTriggerStore, workflowTriggerService)
	if workflowTriggerScheduler != nil {
		workflowTriggerScheduler.Start()
		defer workflowTriggerScheduler.Stop()
	}

	schedulePool := schedules.CreateSchedulePool(
		store,
		&taskPool,
		accessKeyInstallationService,
		encryptionService,
	)

	defer schedulePool.Destroy()
	defer taskPool.Stop()

	// --- Active-Active HA Setup ---
	// When HA is enabled, multiple Semaphore nodes share the same Redis-backed
	// task state and coordinate via Pub/Sub. The following components ensure:
	// 1. Node registry: heartbeat-based cluster membership
	// 2. Schedule deduplication: only one node fires each schedule occurrence
	// 3. WebSocket broadcaster: real-time events reach clients on all nodes
	// 4. Orphan cleaner: expired task controls are recovered from stable runner evidence
	if nodeRegistry := proHA.NewNodeRegistry(store); nodeRegistry != nil {
		if err := nodeRegistry.Start(); err != nil {
			log.WithError(err).Fatal("failed to start HA node registry")
		}
		defer nodeRegistry.Stop()
		log.WithField("node_id", nodeRegistry.NodeID()).Info("HA active-active mode enabled")
	}

	// Task ownership is registered before runners can observe an assignment.
	// The same component relinquishes every lease before this node is marked
	// draining in the cluster registry.
	orphanCleaner := proHA.NewOrphanCleaner(store, &taskPool)
	if orphanCleaner != nil {
		orphanCleaner.Start()
		defer orphanCleaner.Stop()
	}

	// Stop new scheduled starts before relinquishing task and workflow owners.
	// SetNodeDraining waits for every in-flight transition before persisting the
	// draining state that removes this node from coordinated work readiness.
	dedup := proHA.NewScheduleDeduplicator(store)
	if dedup != nil {
		schedulePool.SetDeduplicator(dedup)
	}
	drainers := []pro_interfaces.ClusterDrainer{orphanCleaner, workflowRunLocker}
	if drainer, ok := workflowTriggerScheduler.(pro_interfaces.ClusterDrainer); ok {
		drainers = append([]pro_interfaces.ClusterDrainer{drainer}, drainers...)
	}
	if drainer, ok := dedup.(pro_interfaces.ClusterDrainer); ok {
		drainers = append([]pro_interfaces.ClusterDrainer{drainer}, drainers...)
	}

	// Cluster inspector powers the admin Cluster Dashboard and the public
	// load-balancer readiness endpoint. It is nil when HA is disabled.
	clusterInspector := proHA.NewClusterInspector(store, drainers...)

	// Each process holds its own in-memory cron table. Schedule CRUD handlers only
	// call Refresh on the node that served the HTTP request, so other HA nodes
	// would keep stale jobs until restart. Reload from the shared DB on an interval.
	if util.HAEnabled() {
		const haSchedulePoolSyncInterval = 60 * time.Second
		go func() {
			ticker := time.NewTicker(haSchedulePoolSyncInterval)
			defer ticker.Stop()
			for range ticker.C {
				schedulePool.Refresh()
			}
		}()
	}

	// The workflow reconciler periodically progresses non-terminal runs so
	// approval timeouts fire and statuses converge without a browser poll or a
	// task completion. Cluster-safe: each pass takes the per-run lock. Nil in
	// the open-source build (workflows are Pro-gated).
	if workflowReconciler := proServer.NewWorkflowReconciler(workflowStore, workflowService); workflowReconciler != nil {
		workflowReconciler.Start()
		defer workflowReconciler.Stop()
	}

	util.Config.PrintDbInfo()

	port := util.Config.Port

	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	fmt.Printf("Tmp Path (projects home) %v\n", util.Config.TmpPath)
	fmt.Printf("Semaphore %v\n", util.Version())
	fmt.Printf("Interface %v\n", util.Config.Interface)
	fmt.Printf("Port %v\n", util.Config.Port)

	subscriptionService.StartValidationCron()

	// Start the WebSocket hub before the broadcaster so that h.broadcast
	// channel is being consumed when LocalBroadcast is called.
	go sockets.StartWS()

	if wsBroadcaster := proHA.NewWSBroadcaster(store); wsBroadcaster != nil {
		sockets.SetBroadcaster(wsBroadcaster)
		wsBroadcaster.Start()
		defer wsBroadcaster.Stop()
	}

	go schedulePool.Run()
	go taskPool.Run()

	secretStorageSyncScheduler.Start()
	defer secretStorageSyncScheduler.Stop()
	ldapGroupScheduler.Start()
	defer ldapGroupScheduler.Stop()

	route := api.Route(
		store,
		terraformStore,
		workflowStore,
		ansibleTaskRepo,
		&taskPool,
		projectService,
		integrationService,
		encryptionService,
		accessKeyInstallationService,
		secretStorageService,
		accessKeyService,
		environmentService,
		subscriptionService,
		jwtSigner,
		runnerService,
		workflowService,
		workflowDefinitionService,
		workflowTriggerService,
		logWriteService,
		auditWebhookService,
		appMetrics,
	)

	route.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = helpers.SetContextValue(r, "store", store)
			r = helpers.SetContextValue(r, "schedule_pool", schedulePool)
			r = helpers.SetContextValue(r, "task_pool", &taskPool)
			r = helpers.SetContextValue(r, "log_writer", logWriteService)
			r = helpers.SetContextValue(r, "cluster_inspector", clusterInspector)
			r = helpers.SetContextValue(r, "task_recovery_manager", orphanCleaner)

			next.ServeHTTP(w, r)
		})
	})

	var router http.Handler = route

	router = handlers.ProxyHeaders(router)
	http.Handle("/", router)

	fmt.Println("Server is running")

	defer store.Close()

	var err error
	if util.Config.TLS.Enabled {

		if util.Config.TLS.HTTPRedirectPort != nil && util.Config.TLS.HTTPRedirectAddr != "" {
			panic("You can't use both HTTP redirect address and port at the same time")
		}

		var httpRedirectAddr string

		if util.Config.TLS.HTTPRedirectPort != nil {
			httpRedirectAddr = fmt.Sprintf(":%d", *util.Config.TLS.HTTPRedirectPort)
		} else if util.Config.TLS.HTTPRedirectAddr != "" {
			httpRedirectAddr = util.Config.TLS.HTTPRedirectAddr
		}

		if httpRedirectAddr != "" {

			go func() {

				err = http.ListenAndServe(httpRedirectAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := "https://"

					if util.Config.WebHost != "" {
						webHost, err2 := url.Parse(util.Config.WebHost)
						if err2 != nil {
							log.Panic(err2)
						}
						target += webHost.Host + r.URL.Path
					} else {
						hostParts := strings.Split(r.Host, ":")
						host := hostParts[0]
						target += host + port + r.URL.Path
					}

					if len(r.URL.RawQuery) > 0 {
						target += "?" + r.URL.RawQuery
					}

					if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
						http.Error(w, "http requests forbidden", http.StatusForbidden)
						return
					}

					http.Redirect(w, r, target, http.StatusTemporaryRedirect)
				}))
				if err != nil {
					log.Panic(err)
				}
			}()
		}

		err = http.ListenAndServeTLS(util.Config.Interface+port, util.Config.TLS.CertFile, util.Config.TLS.KeyFile, cropTrailingSlashMiddleware(router))

		if err != nil {
			log.Panic(err)
		}

	} else {
		err = http.ListenAndServe(util.Config.Interface+port, cropTrailingSlashMiddleware(router))
	}

	if err != nil {
		log.WithError(err).Panic("Error starting server")
	}
}

func createStoreWithMigrationVersion(token string, undoTo *string, applyTo *string) db.Store {
	store, _ := createStoreWithMigrationVersionAndConfigPath(token, undoTo, applyTo)
	return store
}

func createStore(token string) db.Store {
	return createStoreWithMigrationVersion(token, nil, nil)
}
