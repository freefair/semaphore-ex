package api

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/api/runners"
	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/api/tasks"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/jwt"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pkg/tz"
	proApi "github.com/semaphoreui/semaphore/pro/api"
	proProjects "github.com/semaphoreui/semaphore/pro/api/projects"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	proHA "github.com/semaphoreui/semaphore/pro/services/ha"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	auditServices "github.com/semaphoreui/semaphore/services/audit"
	capabilityServices "github.com/semaphoreui/semaphore/services/capabilities"
	identityServices "github.com/semaphoreui/semaphore/services/identity"
	"github.com/semaphoreui/semaphore/services/server"
	taskServices "github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

var startTime = tz.Now()

//go:embed public/*
var publicAssets embed.FS

// StoreMiddleware WTF?
func StoreMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// JSONMiddleware ensures that all the routes respond with Json, this is added by default to all routes
func JSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// plainTextMiddleware resets headers to Plain Text if needed
func plainTextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}

func pongHandler(w http.ResponseWriter, r *http.Request) {
	//nolint: errcheck
	w.Write([]byte("pong"))
}

// DelayMiddleware adds artificial delay to simulate slow network conditions
func DelayMiddleware(delay time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(delay)
			next.ServeHTTP(w, r)
		})
	}
}

// Route declares all routes
func Route(
	store db.Store,
	terraformStore db.TerraformStore,
	workflowStore db.WorkflowManager,
	ansibleTaskRepo db.AnsibleTaskRepository,
	taskPool *taskServices.TaskPool,
	projectService server.ProjectService,
	integrationService server.IntegrationService,
	encryptionService server.AccessKeyEncryptionService,
	accessKeyInstallationService server.AccessKeyInstallationService,
	secretStorageService server.SecretStorageService,
	accessKeyService server.AccessKeyService,
	environmentService server.EnvironmentService,
	subscriptionService pro_interfaces.SubscriptionService,
	jwtSigner jwt.Signer,
	runnerService server.RunnerService,
	workflowService pro_interfaces.WorkflowService,
	workflowDefinitionService pro_interfaces.WorkflowDefinitionService,
	workflowTriggerService pro_interfaces.WorkflowTriggerService,
	logWriteService pro_interfaces.LogWriteService,
	auditWebhookService pro_interfaces.AuditWebhookService,
	appMetrics *metrics.Metrics,
	deploymentWindowGovernanceService pro_interfaces.DeploymentWindowGovernanceServiceFacade,
	notificationGovernanceServices ...pro_interfaces.NotificationGovernanceServiceFacade,
) *mux.Router {

	projectController := &projects.ProjectController{ProjectService: projectService}
	runnerController := runners.NewRunnerController(
		store, taskPool, encryptionService, jwtSigner, proHA.NewTaskExecutionEvidenceRecorder(store),
	)
	runnerController.SetMetrics(appMetrics)
	jwksController := NewJwksController(jwtSigner)
	integrationController := NewIntegrationController(store, integrationService)
	environmentController := projects.NewEnvironmentController(store, encryptionService, accessKeyService, environmentService, secretStorageService)
	capabilityProvider := proFeatures.NewCapabilityProvider(store)
	totpService := proFeatures.NewTOTPService(store, capabilityProvider)
	if err := totpService.Initialize(context.Background()); err != nil {
		log.WithError(err).Panic("failed to initialize TOTP lifecycle service")
	}
	ldapService := proFeatures.NewLDAPService(store, capabilityProvider, identityServices.NewLDAPClient())
	if err := ldapService.Initialize(context.Background()); err != nil {
		log.WithError(err).Panic("failed to initialize LDAP lifecycle service")
	}
	oidcGroupMappingService := proFeatures.NewOIDCGroupMappingService(store)
	secretStorageController := projects.NewSecretStorageController(store, secretStorageService, capabilityProvider)
	repositoryController := projects.NewRepositoryController(accessKeyInstallationService)
	keyController := projects.NewKeyController(accessKeyService)
	projectsController := projects.NewProjectsController(accessKeyService)
	terraformController := proApi.NewTerraformController(encryptionService, terraformStore, store)
	terraformInventoryController := proProjects.NewTerraformInventoryController(terraformStore)
	workflowController := proProjects.NewWorkflowController(workflowService, workflowStore, workflowDefinitionService)
	crossProjectTemplateController := proProjects.NewCrossProjectTemplateController(proServer.NewCrossProjectTemplateService(store, workflowStore))
	workflowTriggerController := proProjects.NewWorkflowTriggerController(workflowTriggerService)
	workflowMiddlewareController := projects.NewWorkflowController(workflowStore)
	backupController := projects.NewBackupController(workflowStore)
	userController := NewUserController(subscriptionService)
	usersController := NewUsersController(subscriptionService)
	subscriptionController := proApi.NewSubscriptionController(store, store, store, terraformStore)
	globalRunnerController := NewGlobalRunnerController(runnerService)
	executorImageResolver := capabilityServices.NewExecutorImageResolver(subscriptionService)
	if taskPool != nil {
		taskPool.SetExecutorImageCapabilityResolver(executorImageResolver)
	}
	taskController := projects.NewTaskController(store, ansibleTaskRepo, workflowStore)
	rolesController := proApi.NewRolesController(store, capabilityProvider)
	templateController := projects.NewTemplateController(store, store, executorImageResolver)
	templateController.ConfigureCrossProjectDeletionGuard(workflowStore)
	systemInfoController := NewSystemInfoController(subscriptionService)
	capabilityTestService := proFeatures.NewCapabilityTestService(store)
	capabilityFacade := capabilityServices.NewServiceFacade(capabilityProvider, capabilityTestService)
	auditFacade := auditServices.NewServiceFacade(store, logWriteService, appMetrics, auditWebhookService)
	configureWorkflowAudit(workflowService, auditFacade)
	configureCrossProjectTemplateAudit(crossProjectTemplateController, auditFacade)
	configureExecutionPreflightAudit(taskController, auditFacade)
	configureExecutionPreflightAudit(workflowController, auditFacade)
	workflowAudit := EnhancedWorkflowDeniedAuditMiddleware(auditFacade)
	auditWebhookController := NewAuditWebhookController(auditWebhookService, auditFacade)
	var notificationGovernanceService pro_interfaces.NotificationGovernanceServiceFacade
	if len(notificationGovernanceServices) > 0 && notificationGovernanceServices[0] != nil {
		notificationGovernanceService = notificationGovernanceServices[0]
	} else {
		notificationGovernanceService = proServer.NewNotificationGovernanceService(store)
	}
	notificationGovernanceController := NewNotificationGovernanceController(notificationGovernanceService)
	globalCredentialController := NewGlobalCredentialController(proServer.NewGlobalCredentialService(store))
	projectRunnerController := proProjects.NewProjectRunnerController(subscriptionService, runnerService, capabilityProvider, auditFacade)
	capabilityController := NewCapabilityController(capabilityFacade, auditFacade)
	totpController := NewTOTPController(totpService, auditFacade)
	ldapController := NewLDAPController(ldapService, auditFacade)
	oidcGroupMappingController := NewOIDCGroupMappingController(oidcGroupMappingService, auditFacade)
	deploymentWindowController := proProjects.NewDeploymentWindowController(deploymentWindowGovernanceService, workflowStore)
	configureDeploymentWindowAudit(deploymentWindowController, auditFacade)
	configureDeploymentWindowAudit(taskPool, auditFacade)
	configureDeploymentWindowAudit(workflowTriggerService, auditFacade)

	r := mux.NewRouter()
	r.NotFoundHandler = http.HandlerFunc(servePublic)
	r.Use(helpers.CorrelationMiddleware)

	if util.Config.Debugging.ApiDelay != "" {
		delay, err := time.ParseDuration(util.Config.Debugging.ApiDelay)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"context": "debugging",
			}).Panic("Invalid API delay format")
		}
		r.Use(DelayMiddleware(delay))
	}

	webPath := "/"
	if util.WebHostURL != nil {
		webPath = util.WebHostURL.Path
		if !strings.HasSuffix(webPath, "/") {
			webPath += "/"
		}
	}

	r.Use(mux.CORSMethodMiddleware(r))

	r.Path("/.well-known/jwks.json").Methods("GET", "HEAD").HandlerFunc(jwksController.GetJWKS)

	pingRouter := r.Path(webPath + "api/ping").Subrouter()
	pingRouter.Use(plainTextMiddleware)
	pingRouter.Methods("GET", "HEAD").HandlerFunc(pongHandler)

	readinessRouter := r.Path(webPath + "api/ready").Subrouter()
	readinessRouter.Use(JSONMiddleware)
	readinessRouter.Methods("GET", "HEAD").HandlerFunc(readinessHandler)

	metricsRouter := r.Path(webPath + "api/metrics").Subrouter()
	metricsRouter.Use(metricsAuthMiddleware)
	metricsRouter.Methods("GET", "HEAD").Handler(appMetrics)

	publicAPIRouter := r.PathPrefix(webPath + "api").Subrouter()
	publicAPIRouter.Use(StoreMiddleware, JSONMiddleware)

	publicAPIRouter.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		loginWithIdentityServices(totpService, ldapService, auditFacade, w, r)
	}).Methods("GET", "POST")
	totpSessionAPI := publicAPIRouter.NewRoute().Subrouter()
	totpSessionAPI.Use(csrfProtectionMiddleware)
	totpSessionAPI.HandleFunc("/auth/verify", totpController.VerifySession).Methods("POST")
	totpSessionAPI.HandleFunc("/auth/recovery", totpController.RecoverSession).Methods("POST")
	totpSessionAPI.HandleFunc("/auth/totp/enroll", totpController.BeginSessionEnrollment).Methods("POST")
	totpSessionAPI.HandleFunc("/auth/totp/enroll/{totp_id}/qr", totpController.SessionQR).Methods("GET")
	totpSessionAPI.HandleFunc("/auth/totp/enroll/{totp_id}/confirm", totpController.ConfirmSessionEnrollment).Methods("POST")
	totpSessionAPI.HandleFunc("/auth/totp/enroll/{totp_id}/recovery-codes/acknowledge", totpController.AcknowledgeSessionRecoveryCodes).Methods("POST")

	publicAPIRouter.HandleFunc("/auth/logout", logout).Methods("POST")
	publicAPIRouter.HandleFunc("/auth/oidc/{provider}/login", oidcLogin).Methods("GET", "POST")
	publicAPIRouter.HandleFunc("/auth/oidc/{provider}/redirect", func(w http.ResponseWriter, r *http.Request) {
		oidcRedirectWithIdentityServices(totpService, oidcGroupMappingService, w, r)
	}).Methods("GET")
	publicAPIRouter.HandleFunc("/auth/oidc/{provider}/redirect/{redirect_path:.*}", func(w http.ResponseWriter, r *http.Request) {
		oidcRedirectWithIdentityServices(totpService, oidcGroupMappingService, w, r)
	}).Methods("GET")

	internalAPI := publicAPIRouter.PathPrefix("/internal").Subrouter()
	internalAPI.HandleFunc("/runners", runners.RegisterRunner).Methods("POST")

	runnersAPI := internalAPI.PathPrefix("/runners").Subrouter()
	runnersAPI.Use(runners.RunnerMiddleware)
	runnersAPI.Path("").HandlerFunc(runnerController.GetRunner).Methods("GET", "HEAD")
	runnersAPI.Path("").HandlerFunc(runnerController.UpdateRunner).Methods("PUT")
	runnersAPI.Path("").HandlerFunc(runners.UnregisterRunner).Methods("DELETE")

	publicWebHookRouter := r.PathPrefix(webPath + "api").Subrouter()
	publicWebHookRouter.Use(StoreMiddleware, JSONMiddleware)
	publicWebHookRouter.Path("/integrations/{integration_alias}").HandlerFunc(
		integrationController.ReceiveIntegration).Methods("POST", "GET", "OPTIONS")
	publicWebHookRouter.Path("/workflow-triggers/{project_id}/{workflow_id}/{trigger_id}/api").HandlerFunc(
		workflowTriggerController.InvokeAPITrigger).Methods("POST")
	publicWebHookRouter.Path("/workflow-triggers/{project_id}/{workflow_id}/{trigger_id}/webhook").HandlerFunc(
		workflowTriggerController.InvokeWebhookTrigger).Methods("POST")

	terraformWebhookRouter := publicWebHookRouter.PathPrefix("/terraform").Subrouter()
	terraformWebhookRouter.Use(terraformController.TerraformInventoryAliasMiddleware)
	terraformWebhookRouter.Path("/{alias}").HandlerFunc(terraformController.GetTerraformState).Methods("GET")
	terraformWebhookRouter.Path("/{alias}").HandlerFunc(terraformController.AddTerraformState).Methods("POST")
	terraformWebhookRouter.Path("/{alias}").HandlerFunc(terraformController.LockTerraformState).Methods("LOCK")
	terraformWebhookRouter.Path("/{alias}").HandlerFunc(terraformController.UnlockTerraformState).Methods("UNLOCK")

	authenticatedWS := r.PathPrefix(webPath + "api").Subrouter()
	authenticatedWS.Use(JSONMiddleware, authenticationWithStore)
	authenticatedWS.Path("/ws").HandlerFunc(sockets.Handler).Methods("GET", "HEAD")

	authenticatedAPI := r.PathPrefix(webPath + "api").Subrouter()
	authenticatedAPI.Use(
		EnhancedAnonymousAuditMiddleware(auditFacade),
		csrfProtectionMiddleware,
		StoreMiddleware,
		JSONMiddleware,
		authentication,
	)

	authenticatedAPI.Path("/info").Handler(
		capabilityController.SnapshotMiddleware(http.HandlerFunc(systemInfoController.GetSystemInfo)),
	).Methods("GET", "HEAD")

	authenticatedAPI.Path("/capabilities/lifecycle-test/records").Handler(
		capabilityController.SnapshotMiddleware(
			capabilityController.Require(pro_interfaces.CapabilityAccessRead)(
				http.HandlerFunc(capabilityController.ListRecords),
			),
		),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/capabilities/lifecycle-test/records").Handler(
		capabilityController.SnapshotMiddleware(
			capabilityController.Require(pro_interfaces.CapabilityAccessWrite)(
				http.HandlerFunc(capabilityController.CreateRecord),
			),
		),
	).Methods("POST")
	authenticatedAPI.Path("/capabilities/lifecycle-test/background-actions").Handler(
		capabilityController.SnapshotMiddleware(
			capabilityController.Require(pro_interfaces.CapabilityAccessExecute)(
				http.HandlerFunc(capabilityController.RunBackgroundAction),
			),
		),
	).Methods("POST")

	delegatedProjectRolesSnapshot := capabilityController.DelegatedProjectRolesSnapshotMiddleware
	globalSystemPermission := globalPermissionMiddleware(db.CanManageGlobalSystem)
	authenticatedAPI.Path("/projects").HandlerFunc(projects.GetProjects).Methods("GET", "HEAD")
	authenticatedAPI.Path("/projects").HandlerFunc(projectsController.AddProject).Methods("POST")
	authenticatedAPI.Path("/projects/restore").HandlerFunc(backupController.Restore).Methods("POST")
	authenticatedAPI.Path("/events").HandlerFunc(getAllEvents).Methods("GET", "HEAD")
	authenticatedAPI.HandleFunc("/events/last", getLastEvents).Methods("GET", "HEAD")
	authenticatedAPI.Path("/audit/events").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(db.CanReadGlobalAudit)(http.HandlerFunc(getGlobalAuditEvents)))),
	).Methods("GET", "HEAD")
	registerEnhancedGovernanceRoutes(
		authenticatedAPI,
		capabilityController,
		auditFacade,
		notificationGovernanceController,
		globalCredentialController,
		deploymentWindowController,
		delegatedProjectRolesSnapshot,
		globalSystemPermission,
	)

	authenticatedAPI.Path("/users").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			http.HandlerFunc(usersController.GetUsers))),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/users").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			http.HandlerFunc(usersController.AddUser))),
	).Methods("POST")
	authenticatedAPI.Path("/user").HandlerFunc(userController.GetUser).Methods("GET", "HEAD")

	globalRolesAPI := authenticatedAPI.PathPrefix("/roles").Subrouter()
	globalRolesAPI.Use(
		delegatedProjectRolesSnapshot,
		EnhancedGlobalPermissionAuditMiddleware(auditFacade),
		globalPermissionMiddleware(db.CanManageGlobalRoles),
	)
	globalRolesAPI.Path("/permissions").HandlerFunc(rolesController.GetGlobalPermissionCatalog).Methods("GET", "HEAD")
	globalRolesAPI.Path("").HandlerFunc(rolesController.GetRoles).Methods("GET", "HEAD")
	globalRolesAPI.Path("").HandlerFunc(rolesController.AddRole).Methods("POST")
	globalRolesAPI.Path("/{role_id}").HandlerFunc(rolesController.GetGlobalRole).Methods("GET", "HEAD")
	globalRolesAPI.Path("/{role_id}").HandlerFunc(rolesController.UpdateRole).Methods("PUT", "POST")
	globalRolesAPI.Path("/{role_id}").HandlerFunc(rolesController.DeleteRole).Methods("DELETE")

	globalRoleAssignmentsAPI := authenticatedAPI.PathPrefix("/users/{user_id}/global-roles").Subrouter()
	globalRoleAssignmentsAPI.Use(
		delegatedProjectRolesSnapshot,
		EnhancedGlobalPermissionAuditMiddleware(auditFacade),
		globalPermissionMiddleware(db.CanManageGlobalRoles),
	)
	globalRoleAssignmentsAPI.Path("").HandlerFunc(rolesController.GetGlobalRoleAssignments).Methods("GET", "HEAD")
	globalRoleAssignmentsAPI.Path("").HandlerFunc(rolesController.AddGlobalRoleAssignment).Methods("POST")
	globalRoleAssignmentsAPI.Path("/{assignment_id}").HandlerFunc(rolesController.DeleteGlobalRoleAssignment).Methods("DELETE")
	authenticatedAPI.Path("/users/{user_id}/global-permissions").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(db.CanManageGlobalRoles)(
				http.HandlerFunc(rolesController.GetEffectiveGlobalPermissions),
			))),
	).Methods("GET", "HEAD")

	authenticatedAPI.Path("/apps").HandlerFunc(getApps).Methods("GET", "HEAD")

	tokenAPI := authenticatedAPI.PathPrefix("/user").Subrouter()
	tokenAPI.Path("/tokens").HandlerFunc(getAPITokens).Methods("GET", "HEAD")
	tokenAPI.Path("/tokens").HandlerFunc(createAPIToken).Methods("POST")
	tokenAPI.HandleFunc("/tokens/{token_id}", deleteAPIToken).Methods("DELETE")
	tokenAPI.Path("/options").HandlerFunc(getUserOptions).Methods("GET", "HEAD")
	tokenAPI.Path("/options").HandlerFunc(setUserOption).Methods("POST")
	tokenAPI.Path("/identities/ldap").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		linkLdapIdentityWithService(ldapService, auditFacade, w, r)
	}).Methods("POST")

	globalSystemAPI := authenticatedAPI.NewRoute().Subrouter()
	globalSystemAPI.Use(
		delegatedProjectRolesSnapshot,
		EnhancedGlobalPermissionAuditMiddleware(auditFacade),
		globalSystemPermission,
	)
	globalSystemAPI.Path("/options").HandlerFunc(getOptions).Methods("GET", "HEAD")
	globalSystemAPI.Path("/options").HandlerFunc(setOption).Methods("POST")
	globalSystemAPI.Path("/cache").HandlerFunc(clearCache).Methods("DELETE", "HEAD")

	adminAPI := authenticatedAPI.NewRoute().Subrouter()
	adminAPI.Use(EnhancedAdminAuditMiddleware(auditFacade), adminMiddleware)
	adminAPI.Path("/admin/info").HandlerFunc(getAdminInfo).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/lifecycle-test").HandlerFunc(capabilityController.Configure).Methods("PUT")
	adminAPI.Path("/capabilities/runtime-secrets").HandlerFunc(capabilityController.ConfigureRuntimeSecrets).Methods("PUT")
	adminAPI.Path("/capabilities/totp").HandlerFunc(totpController.Configure).Methods("PUT")
	adminAPI.Path("/capabilities/totp").HandlerFunc(totpController.Configuration).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/totp/transitions").HandlerFunc(totpController.Transitions).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap").HandlerFunc(ldapController.Providers).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap").HandlerFunc(ldapController.Configure).Methods("PUT")
	adminAPI.Path("/capabilities/ldap/test").HandlerFunc(ldapController.Test).Methods("POST")
	adminAPI.Path("/capabilities/ldap/state").HandlerFunc(ldapController.SetState).Methods("PUT")
	adminAPI.Path("/capabilities/ldap/transitions").HandlerFunc(ldapController.Transitions).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap/group-mappings").HandlerFunc(ldapController.GroupMappings).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap/group-mappings/{mapping_id}").HandlerFunc(ldapController.SaveGroupMapping).Methods("PUT")
	adminAPI.Path("/capabilities/ldap/group-mappings/{mapping_id}").HandlerFunc(ldapController.DeleteGroupMapping).Methods("DELETE")
	adminAPI.Path("/capabilities/ldap/group-mappings/preview").HandlerFunc(ldapController.PreviewGroupMappings).Methods("POST")
	adminAPI.Path("/capabilities/ldap/group-mappings/apply").HandlerFunc(ldapController.ApplyGroupPreview).Methods("POST")
	adminAPI.Path("/capabilities/ldap/group-mappings/reconcile").HandlerFunc(ldapController.ReconcileGroupMappings).Methods("POST")
	adminAPI.Path("/capabilities/ldap/group-mappings/history").HandlerFunc(ldapController.GroupReconciliationHistory).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mapping/providers").HandlerFunc(oidcGroupMappingController.Providers).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mappings").HandlerFunc(oidcGroupMappingController.GroupMappings).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mappings/{mapping_id}").HandlerFunc(oidcGroupMappingController.SaveGroupMapping).Methods("PUT")
	adminAPI.Path("/capabilities/oidc/group-mappings/{mapping_id}").HandlerFunc(oidcGroupMappingController.DeleteGroupMapping).Methods("DELETE")
	adminAPI.Path("/capabilities/oidc/group-mappings/preview").HandlerFunc(oidcGroupMappingController.PreviewGroupMappings).Methods("POST")
	adminAPI.Path("/capabilities/oidc/group-mappings/history").HandlerFunc(oidcGroupMappingController.GroupReconciliationHistory).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mappings/assignments").HandlerFunc(oidcGroupMappingController.EffectiveGroupAssignments).Methods("GET", "HEAD")
	adminAPI.Path("/audit-webhook").HandlerFunc(auditWebhookController.GetConfiguration).Methods("GET", "HEAD")
	adminAPI.Path("/audit-webhook").HandlerFunc(auditWebhookController.Configure).Methods("PUT")
	adminAPI.Path("/audit-webhook/test").HandlerFunc(auditWebhookController.TestDelivery).Methods("POST")
	adminAPI.Path("/audit-webhook/pause").HandlerFunc(auditWebhookController.Pause).Methods("POST")
	adminAPI.Path("/audit-webhook/resume").HandlerFunc(auditWebhookController.Resume).Methods("POST")
	adminAPI.Path("/audit-webhook/deliveries").HandlerFunc(auditWebhookController.History).Methods("GET", "HEAD")

	adminAPI.Path("/cluster").HandlerFunc(getClusterStatus).Methods("GET", "HEAD")
	adminAPI.Path("/cluster/nodes").HandlerFunc(getClusterNodes).Methods("GET", "HEAD")
	adminAPI.Path("/cluster/nodes/{boot_id}").HandlerFunc(getClusterNode).Methods("GET", "HEAD")
	adminAPI.Path("/cluster/nodes/{boot_id}/draining").HandlerFunc(setClusterNodeDraining).Methods("POST")
	adminAPI.Path("/cluster/tasks").HandlerFunc(getClusterTasks).Methods("GET", "HEAD")
	adminAPI.Path("/cluster/tasks").HandlerFunc(clearClusterTasks).Methods("DELETE")

	adminAPI.Path("/runners").HandlerFunc(globalRunnerController.GetRunners).Methods("GET", "HEAD")
	adminAPI.Path("/runners").HandlerFunc(globalRunnerController.AddRunner).Methods("POST", "HEAD")
	adminAPI.Path("/runner_tags").HandlerFunc(globalRunnerController.GetRunnerTags).Methods("GET", "HEAD")
	adminAPI.Path("/runners/docker-policy").HandlerFunc(globalRunnerController.GetDockerExecutionPolicy).Methods("GET", "HEAD")
	adminAPI.Path("/runners/docker-policy").HandlerFunc(globalRunnerController.UpdateDockerExecutionPolicy).Methods("PUT")
	adminAPI.Path("/runners/docker-policy/test").HandlerFunc(globalRunnerController.TestDockerExecutionPolicy).Methods("POST")
	adminAPI.Path("/runners/kubernetes-policies/{cluster_alias}").HandlerFunc(globalRunnerController.GetKubernetesExecutionPolicy).Methods("GET", "HEAD")
	adminAPI.Path("/runners/kubernetes-policies/{cluster_alias}").HandlerFunc(globalRunnerController.UpdateKubernetesExecutionPolicy).Methods("PUT")
	adminAPI.Path("/runners/kubernetes-policies/{cluster_alias}/test").HandlerFunc(globalRunnerController.TestKubernetesExecutionPolicy).Methods("POST")

	globalRunnersAPI := adminAPI.PathPrefix("/runners").Subrouter()
	globalRunnersAPI.Use(globalRunnerController.RunnerMiddleware)
	globalRunnersAPI.Path("/{runner_id}").HandlerFunc(globalRunnerController.GetRunner).Methods("GET", "HEAD")
	globalRunnersAPI.Path("/{runner_id}").HandlerFunc(globalRunnerController.UpdateRunner).Methods("PUT", "POST")
	globalRunnersAPI.Path("/{runner_id}/active").HandlerFunc(globalRunnerController.SetRunnerActive).Methods("POST")
	globalRunnersAPI.Path("/{runner_id}/registration-token").HandlerFunc(globalRunnerController.RegenerateRegistrationToken).Methods("POST")
	globalRunnersAPI.Path("/{runner_id}").HandlerFunc(globalRunnerController.DeleteRunner).Methods("DELETE")
	globalRunnersAPI.Path("/{runner_id}/cache").HandlerFunc(globalRunnerController.ClearRunnerCache).Methods("DELETE")
	globalRunnersAPI.Path("/{runner_id}/docker-reconciliation/quarantines").HandlerFunc(globalRunnerController.GetDockerReconciliationQuarantines).Methods("GET", "HEAD")
	globalRunnersAPI.Path("/{runner_id}/docker-reconciliation/candidates").HandlerFunc(globalRunnerController.GetDockerReconciliationCandidates).Methods("GET", "HEAD")
	globalRunnersAPI.Path("/{runner_id}/docker-reconciliation/diagnostics").HandlerFunc(globalRunnerController.GetDockerReconciliationDiagnostics).Methods("GET", "HEAD")
	globalRunnersAPI.Path("/{runner_id}/docker-reconciliation/remediation").HandlerFunc(globalRunnerController.RequestDockerReconciliationRemediation).Methods("POST")
	globalRunnersAPI.Path("/{runner_id}/kubernetes-reconciliation/diagnostics").HandlerFunc(globalRunnerController.GetKubernetesReconciliationDiagnostics).Methods("GET", "HEAD")
	globalRunnersAPI.Path("/{runner_id}/kubernetes-reconciliation/remediation").HandlerFunc(globalRunnerController.RequestKubernetesReconciliationRemediation).Methods("POST")

	appsAPI := adminAPI.PathPrefix("/apps").Subrouter()
	appsAPI.Use(appMiddleware)
	appsAPI.Path("/{app_id}").HandlerFunc(getApp).Methods("GET", "HEAD")
	appsAPI.Path("/{app_id}").HandlerFunc(setApp).Methods("PUT", "POST")
	appsAPI.Path("/{app_id}/active").HandlerFunc(setAppActive).Methods("POST")
	appsAPI.Path("/{app_id}").HandlerFunc(deleteApp).Methods("DELETE")

	adminAPI.Path("/tasks").HandlerFunc(tasks.GetTasks).Methods("GET", "HEAD")
	tasksAPI := adminAPI.PathPrefix("/tasks").Subrouter()
	tasksAPI.Use(tasks.TaskMiddleware)
	tasksAPI.Path("/{task_id}").HandlerFunc(tasks.GetTasks).Methods("GET", "HEAD")
	tasksAPI.Path("/{task_id}").HandlerFunc(tasks.DeleteTask).Methods("DELETE")

	userUserAPI := authenticatedAPI.Path("/users/{user_id}").Subrouter()
	userUserAPI.Use(
		delegatedProjectRolesSnapshot,
		EnhancedGlobalPermissionAuditMiddleware(auditFacade),
		usersController.ReadonlyUserMiddleware,
	)
	userUserAPI.Methods("GET", "HEAD").HandlerFunc(userController.GetUser)

	userAPI := authenticatedAPI.Path("/users/{user_id}").Subrouter()
	userAPI.Use(
		delegatedProjectRolesSnapshot,
		EnhancedGlobalPermissionAuditMiddleware(auditFacade),
		usersController.GetUserMiddleware,
	)

	userAPI.Methods("PUT").HandlerFunc(usersController.UpdateUser)
	userAPI.Methods("DELETE").HandlerFunc(usersController.DeleteUser)

	userPasswordAPI := authenticatedAPI.PathPrefix("/users/{user_id}").Subrouter()
	userPasswordAPI.Use(
		delegatedProjectRolesSnapshot,
		usersController.GetUserMiddleware,
	)
	userPasswordAPI.Path("/password").Handler(
		EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			http.HandlerFunc(usersController.UpdateUserPassword)),
	).Methods("POST")
	userPasswordAPI.Path("/2fas/totp").HandlerFunc(totpController.Status).Methods("GET", "HEAD")
	userPasswordAPI.Path("/2fas/totp").HandlerFunc(totpController.BeginEnrollment).Methods("POST")
	userPasswordAPI.Path("/2fas/totp/{totp_id}/qr").HandlerFunc(totpController.QR).Methods("GET")
	userPasswordAPI.Path("/2fas/totp/{totp_id}/confirm").HandlerFunc(totpController.ConfirmEnrollment).Methods("POST")
	userPasswordAPI.Path("/2fas/totp/{totp_id}/recovery-codes/acknowledge").HandlerFunc(totpController.AcknowledgeRecoveryCodes).Methods("POST")
	userPasswordAPI.Path("/2fas/totp/{totp_id}").HandlerFunc(totpController.Reset).Methods("DELETE")
	userPasswordAPI.Path("/identities").HandlerFunc(usersController.GetUserIdentities).Methods("GET", "HEAD")
	userPasswordAPI.Path("/identities/{type}/{provider}").HandlerFunc(usersController.DeleteUserIdentity).Methods("DELETE")

	projectGet := authenticatedAPI.Path("/project/{project_id}").Subrouter()
	projectGet.Use(projects.ProjectMiddleware)
	projectGet.Methods("GET", "HEAD").HandlerFunc(projects.GetProject)

	//
	// Start and Stop tasks
	projectTaskStart := authenticatedAPI.PathPrefix("/project/{project_id}").Subrouter()
	projectTaskStart.Use(projects.ProjectMiddleware, taskController.NewTaskMiddleware, taskController.GetTaskPermissionsMiddleware, projects.GetMustCanMiddleware(db.CanRunProjectTasks))
	projectTaskStart.Path("/tasks").Handler(
		capabilityController.RequireExecutionPreflightForReviewedStart(http.HandlerFunc(taskController.AddTask)),
	).Methods("POST")

	projectTaskPreflight := authenticatedAPI.PathPrefix("/project/{project_id}").Subrouter()
	projectTaskPreflight.Use(
		projects.ProjectMiddleware,
		taskController.NewTaskMiddleware,
		taskController.GetTaskPermissionsMiddleware,
		taskController.GetTaskReadPermissionMiddleware,
		projects.GetMustCanMiddleware(db.CanRunProjectTasks),
		capabilityController.SnapshotMiddleware,
		capabilityController.RequireCapability(
			pro_interfaces.CapabilityExecutionPreflight,
			pro_interfaces.CapabilityAccessRead,
		),
	)
	projectTaskPreflight.Path("/tasks/preflight").HandlerFunc(taskController.PreviewTask).Methods("POST")

	projectTaskStop := authenticatedAPI.PathPrefix("/project/{project_id}").Subrouter()
	projectTaskStop.Use(projects.ProjectMiddleware, taskController.GetTaskMiddleware, taskController.GetTaskPermissionsMiddleware, taskController.WorkflowTaskControlAccessMiddleware)
	projectTaskStop.HandleFunc("/tasks/{task_id}/stop", taskController.StopTask).Methods("POST")
	projectTaskStop.HandleFunc("/tasks/{task_id}/confirm", taskController.ConfirmTask).Methods("POST")
	projectTaskStop.HandleFunc("/tasks/{task_id}/reject", taskController.RejectTask).Methods("POST")
	projectTaskStop.HandleFunc("/tasks/{task_id}/retry-recovery", taskController.RetryTaskRecovery).Methods("POST")

	//
	// Project resources CRUD
	projectUserAPI := authenticatedAPI.PathPrefix("/project/{project_id}").Subrouter()
	projectUserAPI.Use(
		projects.ProjectMiddleware,
		EnhancedProjectPermissionAuditMiddleware(auditFacade),
		projects.GetMustCanMiddleware(db.CanManageProjectResources),
	)

	projectUserAPI.Path("/role").HandlerFunc(projects.GetUserRole).Methods("GET", "HEAD")

	projectUserAPI.Path("/events").HandlerFunc(getAllEvents).Methods("GET", "HEAD")
	projectUserAPI.HandleFunc("/events/last", getLastEvents).Methods("GET", "HEAD")

	projectUserAPI.Path("/users").HandlerFunc(projects.GetUsers).Methods("GET", "HEAD")

	projectUserAPI.Path("/keys").HandlerFunc(projects.GetKeys).Methods("GET", "HEAD")
	projectUserAPI.Path("/keys").HandlerFunc(keyController.AddKey).Methods("POST")
	projectUserAPI.Path("/keys/generate").HandlerFunc(keyController.GenerateSSHKey).Methods("POST")

	projectUserAPI.Path("/secret_storages").HandlerFunc(secretStorageController.GetSecretStorages).Methods("GET", "HEAD")
	projectUserAPI.Path("/secret_storages").HandlerFunc(secretStorageController.Add).Methods("POST")

	projectUserAPI.Path("/inventory").HandlerFunc(projects.GetInventory).Methods("GET", "HEAD")
	projectUserAPI.Path("/inventory").HandlerFunc(projects.AddInventory).Methods("POST")

	projectUserAPI.Path("/environment").HandlerFunc(projects.GetEnvironment).Methods("GET", "HEAD")
	projectUserAPI.Path("/environment").HandlerFunc(environmentController.AddEnvironment).Methods("POST")

	projectUserAPI.Path("/tasks").HandlerFunc(taskController.GetAllTasks).Methods("GET", "HEAD")
	projectUserAPI.HandleFunc("/tasks/last", taskController.GetLastTasks).Methods("GET", "HEAD")

	projectUserAPI.Path("/stats").HandlerFunc(taskController.GetTaskStats).Methods("GET", "HEAD")

	projectUserAPI.Path("/templates").HandlerFunc(projects.GetTemplates).Methods("GET", "HEAD")
	projectUserAPI.Path("/templates").HandlerFunc(templateController.AddTemplate).Methods("POST")
	projectWorkflowCollectionAPI := authenticatedAPI.PathPrefix("/project/{project_id}/workflows").Subrouter()
	projectWorkflowCollectionAPI.Use(projects.ProjectMiddleware, workflowAudit)
	projectWorkflowCollectionAPI.Path("").Handler(
		projects.WorkflowProjectPermissionMiddleware(pro_interfaces.PermissionViewWorkflow)(
			http.HandlerFunc(workflowController.GetWorkflows),
		),
	).Methods("GET", "HEAD")
	projectWorkflowCollectionAPI.Path("").Handler(
		projects.WorkflowProjectPermissionMiddleware(pro_interfaces.PermissionEditWorkflow)(
			http.HandlerFunc(workflowController.AddWorkflow),
		),
	).Methods("POST")
	projectWorkflowCollectionAPI.Path("/validate").Handler(
		projects.WorkflowProjectPermissionMiddleware(pro_interfaces.PermissionEditWorkflow)(
			http.HandlerFunc(workflowController.ValidateWorkflow),
		),
	).Methods("POST")

	projectUserAPI.Path("/schedules").HandlerFunc(projects.GetProjectSchedules).Methods("GET", "HEAD")
	projectUserAPI.Path("/schedules").HandlerFunc(projects.AddSchedule).Methods("POST")
	projectUserAPI.Path("/schedules/validate").HandlerFunc(projects.ValidateScheduleCronFormat).Methods("POST")

	projectUserAPI.Path("/views").HandlerFunc(projects.GetViews).Methods("GET", "HEAD")
	projectUserAPI.Path("/views").HandlerFunc(projects.AddView).Methods("POST")
	projectUserAPI.Path("/views/positions").HandlerFunc(projects.SetViewPositions).Methods("POST")

	projectUserAPI.Path("/integrations").HandlerFunc(projects.GetIntegrations).Methods("GET", "HEAD")
	projectUserAPI.Path("/integrations").HandlerFunc(projects.AddIntegration).Methods("POST")
	projectUserAPI.Path("/backup").HandlerFunc(backupController.GetBackup).Methods("GET", "HEAD")
	projectUserAPI.Path("/notifications/test").HandlerFunc(projectController.SendTestNotification).Methods("POST")

	projectUserAPI.Path("/runners").HandlerFunc(projectRunnerController.GetRunners).Methods("GET", "HEAD")
	projectUserAPI.Path("/runners").HandlerFunc(projectRunnerController.AddRunner).Methods("POST")
	// History intentionally does not require a live runner row: finished task
	// snapshots remain queryable after the runner has been deleted.
	projectUserAPI.Path("/runners/{runner_id}/history").HandlerFunc(projectRunnerController.GetRunnerHistory).Methods("GET", "HEAD")
	projectUserAPI.Path("/runner_tags").HandlerFunc(projectRunnerController.GetRunnerTags).Methods("GET", "HEAD")

	projectRunnersAPI := projectUserAPI.PathPrefix("/runners").Subrouter()
	projectRunnersAPI.Use(projectRunnerController.RunnerMiddleware)
	projectRunnersAPI.Path("/{runner_id}").HandlerFunc(projectRunnerController.GetRunner).Methods("GET", "HEAD")
	projectRunnersAPI.Path("/{runner_id}/health").HandlerFunc(projectRunnerController.GetRunnerHealth).Methods("GET", "HEAD")
	projectRunnersAPI.Path("/{runner_id}").HandlerFunc(projectRunnerController.UpdateRunner).Methods("PUT", "POST")
	projectRunnersAPI.Path("/{runner_id}/active").HandlerFunc(projectRunnerController.SetRunnerActive).Methods("POST")
	projectRunnersAPI.Path("/{runner_id}/registration-token").HandlerFunc(projectRunnerController.RegenerateRegistrationToken).Methods("POST")
	projectRunnersAPI.Path("/{runner_id}").HandlerFunc(projectRunnerController.DeleteRunner).Methods("DELETE")
	projectRunnersAPI.Path("/{runner_id}/cache").HandlerFunc(projectRunnerController.ClearRunnerCache).Methods("DELETE")

	projectRoleOptionsAPI := authenticatedAPI.PathPrefix("/project/{project_id}/roles").Subrouter()
	projectRoleOptionsAPI.Use(projects.ProjectMiddleware)
	projectRoleOptionsAPI.Path("/all").HandlerFunc(rolesController.GetProjectAndGlobalRoles).Methods("GET", "HEAD")

	projectRolesAPI := authenticatedAPI.PathPrefix("/project/{project_id}/roles").Subrouter()
	projectRolesAPI.Use(
		projects.ProjectMiddleware,
		EnhancedProjectPermissionAuditMiddleware(auditFacade),
		projects.GetMustHavePermissionMiddleware(db.CanManageProjectUsers),
	)
	projectRolesAPI.Path("").HandlerFunc(rolesController.GetProjectRoles).Methods("GET", "HEAD")
	projectRolesAPI.Path("").HandlerFunc(rolesController.AddProjectRole).Methods("POST")
	projectRolesAPI.Path("/permissions").HandlerFunc(rolesController.GetProjectPermissionCatalog).Methods("GET", "HEAD")
	projectRolesAPI.Path("/{role_id}").HandlerFunc(rolesController.GetProjectRole).Methods("GET", "HEAD")
	projectRolesAPI.Path("/{role_id}").HandlerFunc(rolesController.UpdateProjectRole).Methods("PUT", "POST")
	projectRolesAPI.Path("/{role_id}").HandlerFunc(rolesController.DeleteProjectRole).Methods("DELETE")

	//
	// Updating and deleting project
	projectAdminAPI := authenticatedAPI.Path("/project/{project_id}").Subrouter()
	projectAdminAPI.Use(projects.ProjectMiddleware, projects.GetMustCanMiddleware(db.CanUpdateProject))
	projectAdminAPI.Methods("PUT").HandlerFunc(projectController.UpdateProject)
	projectAdminAPI.Methods("DELETE").HandlerFunc(projectController.DeleteProject)

	meAPI := authenticatedAPI.Path("/project/{project_id}/me").Subrouter()
	meAPI.Use(projects.ProjectMiddleware)
	meAPI.HandleFunc("", projects.LeftProject).Methods("DELETE")

	cacheAPI := authenticatedAPI.Path("/project/{project_id}/cache").Subrouter()
	cacheAPI.Use(projects.ProjectMiddleware)
	cacheAPI.HandleFunc("", projects.ClearCache).Methods("DELETE")

	//
	// Manage project users
	projectAdminUsersAPI := authenticatedAPI.PathPrefix("/project/{project_id}").Subrouter()

	projectAdminUsersAPI.Use(
		projects.ProjectMiddleware,
		EnhancedProjectPermissionAuditMiddleware(auditFacade),
		projects.GetMustCanMiddleware(db.CanManageProjectUsers),
	)
	projectAdminUsersAPI.Path("/users").HandlerFunc(projects.AddUser).Methods("POST")

	projectUserManagement := projectAdminUsersAPI.PathPrefix("/users").Subrouter()
	projectUserManagement.Use(projects.UserMiddleware)

	projectUserManagement.HandleFunc("/{user_id}", projects.GetUsers).Methods("GET", "HEAD")
	projectUserManagement.HandleFunc("/{user_id}", projects.UpdateUser).Methods("PUT")
	projectUserManagement.HandleFunc("/{user_id}", projects.RemoveUser).Methods("DELETE")

	//
	// Project resources CRUD (continue)
	projectKeyManagement := projectUserAPI.PathPrefix("/keys").Subrouter()
	projectKeyManagement.Use(projects.KeyMiddleware)

	projectKeyManagement.HandleFunc("/{key_id}", projects.GetKeys).Methods("GET", "HEAD")
	projectKeyManagement.HandleFunc("/{key_id}/refs", projects.GetKeyRefs).Methods("GET", "HEAD")
	projectKeyManagement.HandleFunc("/{key_id}/rotate", keyController.RotateGeneratedSSHKey).Methods("POST")
	projectKeyManagement.HandleFunc("/{key_id}", keyController.UpdateKey).Methods("PUT")
	projectKeyManagement.HandleFunc("/{key_id}", keyController.RemoveKey).Methods("DELETE")

	projectSecretStorageManagement := projectUserAPI.PathPrefix("/secret_storages").Subrouter()
	projectSecretStorageManagement.Use(projects.SecretStorageMiddleware)
	projectSecretStorageManagement.HandleFunc("/{storage_id}", secretStorageController.GetSecretStorage).Methods("GET", "HEAD")
	projectSecretStorageManagement.HandleFunc("/{storage_id}/refs", secretStorageController.GetRefs).Methods("GET", "HEAD")
	projectSecretStorageManagement.HandleFunc("/{storage_id}", secretStorageController.Update).Methods("PUT")
	projectSecretStorageManagement.HandleFunc("/{storage_id}", secretStorageController.Remove).Methods("DELETE")
	projectSecretStorageManagement.HandleFunc("/{storage_id}/sync", secretStorageController.SyncSecrets).Methods("POST")
	projectSecretStorageManagement.HandleFunc("/{storage_id}/sync/history", secretStorageController.GetSyncHistory).Methods("GET", "HEAD")
	projectSecretStorageManagement.HandleFunc("/{storage_id}/test", secretStorageController.TestConnection).Methods("POST")

	projectRepositoriesAPI := projectUserAPI.PathPrefix("/repositories").Subrouter()
	projectRepositoriesAPI.Use(projects.GetMustHavePermissionMiddleware(db.CanViewProjectResources))
	projectRepositoriesAPI.Path("").HandlerFunc(projects.GetRepositories).Methods("GET", "HEAD")
	projectRepositoriesAPI.Path("").HandlerFunc(projects.AddRepository).Methods("POST")

	projectRepoManagement := projectRepositoriesAPI.PathPrefix("/{repository_id}").Subrouter()
	projectRepoManagement.Use(projects.RepositoryMiddleware)

	projectRepoManagement.Path("").HandlerFunc(projects.GetRepositories).Methods("GET", "HEAD")
	projectRepoManagement.Path("/refs").HandlerFunc(projects.GetRepositoryRefs).Methods("GET", "HEAD")
	projectRepoManagement.Path("").HandlerFunc(projects.UpdateRepository).Methods("PUT")
	projectRepoManagement.Path("").HandlerFunc(projects.RemoveRepository).Methods("DELETE")
	projectRepoManagement.Path("/branches").HandlerFunc(repositoryController.GetRepositoryBranches).Methods("GET", "HEAD")
	projectRepoManagement.Path("/playbooks").HandlerFunc(repositoryController.GetRepositoryPlaybooks).Methods("GET", "HEAD")

	projectInventoryManagement := projectUserAPI.PathPrefix("/inventory").Subrouter()
	projectInventoryManagement.Use(projects.InventoryMiddleware)

	projectInventoryManagement.HandleFunc("/{inventory_id}", projects.GetInventory).Methods("GET", "HEAD")
	projectInventoryManagement.HandleFunc("/{inventory_id}/refs", projects.GetInventoryRefs).Methods("GET", "HEAD")
	projectInventoryManagement.HandleFunc("/{inventory_id}", projects.UpdateInventory).Methods("PUT")
	projectInventoryManagement.HandleFunc("/{inventory_id}", projects.RemoveInventory).Methods("DELETE")

	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/aliases", terraformInventoryController.GetTerraformInventoryAliases).Methods("GET", "HEAD")
	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/aliases", terraformInventoryController.AddTerraformInventoryAlias).Methods("POST")
	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/aliases/{alias_id}", terraformInventoryController.GetTerraformInventoryAlias).Methods("GET")
	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/aliases/{alias_id}", terraformInventoryController.DeleteTerraformInventoryAlias).Methods("DELETE")
	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/aliases/{alias_id}", terraformInventoryController.SetTerraformInventoryAliasAccessKey).Methods("PUT")

	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/states", terraformInventoryController.GetTerraformInventoryStates).Methods("GET", "HEAD")
	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/states/latest", terraformInventoryController.GetTerraformInventoryLatestState).Methods("GET", "HEAD")
	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/states/{state_id}", terraformInventoryController.GetTerraformInventoryState).Methods("GET")
	projectInventoryManagement.HandleFunc("/{inventory_id}/terraform/states/{state_id}", terraformInventoryController.DeleteTerraformInventoryState).Methods("DELETE")

	projectEnvManagement := projectUserAPI.PathPrefix("/environment").Subrouter()
	projectEnvManagement.Use(environmentController.EnvironmentMiddleware)

	projectEnvManagement.HandleFunc("/{environment_id}", projects.GetEnvironment).Methods("GET", "HEAD")
	projectEnvManagement.HandleFunc("/{environment_id}/refs", projects.GetEnvironmentRefs).Methods("GET", "HEAD")
	projectEnvManagement.HandleFunc("/{environment_id}", environmentController.UpdateEnvironment).Methods("PUT")
	projectEnvManagement.HandleFunc("/{environment_id}", environmentController.RemoveEnvironment).Methods("DELETE")
	projectEnvManagement.HandleFunc("/{environment_id}/sync", environmentController.SyncEnvironment).Methods("POST")

	projectTmplManagement := projectUserAPI.PathPrefix("/templates").Subrouter()
	projectTmplManagement.Use(
		delegatedProjectRolesSnapshot,
		capabilityController.RequireDelegatedProjectRolesForRequest,
		projects.TemplatesMiddleware,
	)
	templateRead := projects.GetMustHaveTemplatePermissionMiddleware(db.CanReadTemplate)
	templateRun := projects.GetMustHaveTemplatePermissionMiddleware(db.CanRunTemplate)
	templateEdit := projects.GetMustHaveTemplatePermissionMiddleware(db.CanEditTemplate)
	templateDelete := projects.GetMustHaveTemplatePermissionMiddleware(db.CanDeleteTemplate)
	templateACLManage := projects.GetMustHaveBaseProjectPermissionMiddleware(db.CanManageProjectResources)
	projectTmplManagement.Path("/{template_id}/versions").Handler(templateACLManage(templateEdit(http.HandlerFunc(crossProjectTemplateController.PublishTemplateVersion)))).Methods("POST")
	projectTmplManagement.Path("/{template_id}/versions").Handler(templateACLManage(templateEdit(http.HandlerFunc(crossProjectTemplateController.ListTemplateVersions)))).Methods("GET", "HEAD")
	projectTmplManagement.Path("/{template_id}/cross-project-grants").Handler(templateACLManage(http.HandlerFunc(crossProjectTemplateController.CreateGrant))).Methods("POST")

	projectCrossProjectTemplates := authenticatedAPI.PathPrefix("/project/{project_id}/cross-project-template-grants").Subrouter()
	projectCrossProjectTemplates.Use(projects.ProjectMiddleware, templateACLManage)
	projectCrossProjectTemplates.HandleFunc("", crossProjectTemplateController.ListGrants).Methods("GET", "HEAD")
	projectCrossProjectTemplates.HandleFunc("/{grant_id}", crossProjectTemplateController.UpdateGrant).Methods("PUT")
	projectCrossProjectTemplates.HandleFunc("/{grant_id}", crossProjectTemplateController.DeleteGrant).Methods("DELETE")
	projectCrossProjectTemplates.HandleFunc("/{grant_id}/accept", crossProjectTemplateController.AcceptGrant).Methods("POST")
	projectCrossProjectTemplates.HandleFunc("/{grant_id}/revoke", crossProjectTemplateController.RevokeGrant).Methods("POST")
	projectCrossProjectTemplates.HandleFunc("/{grant_id}/references", crossProjectTemplateController.ListReferences).Methods("GET", "HEAD")

	projectTmplManagement.Path("/{template_id}").Handler(
		templateEdit(http.HandlerFunc(templateController.UpdateTemplate))).Methods("PUT")
	projectTmplManagement.Path("/{template_id}/description").Handler(
		templateEdit(http.HandlerFunc(projects.UpdateTemplateDescription))).Methods("PUT")
	projectTmplManagement.Path("/{template_id}").Handler(
		templateDelete(http.HandlerFunc(templateController.RemoveTemplate))).Methods("DELETE")
	projectTmplManagement.Path("/{template_id}").Handler(
		templateRead(http.HandlerFunc(projects.GetTemplate))).Methods("GET")
	projectTmplManagement.Path("/{template_id}/refs").Handler(
		templateRead(http.HandlerFunc(projects.GetTemplateRefs))).Methods("GET", "HEAD")
	projectTmplManagement.Path("/{template_id}/tasks").Handler(
		templateRead(http.HandlerFunc(taskController.GetAllTasks))).Methods("GET")
	projectTmplManagement.Path("/{template_id}/tasks/last").Handler(
		templateRead(http.HandlerFunc(taskController.GetLastTasks))).Methods("GET")
	projectTmplManagement.Path("/{template_id}/schedules").Handler(
		templateRead(http.HandlerFunc(projects.GetTemplateSchedules))).Methods("GET")
	projectTmplManagement.Path("/{template_id}/stats").Handler(
		templateRead(http.HandlerFunc(taskController.GetTaskStats))).Methods("GET")
	projectTmplManagement.Path("/{template_id}/permissions/effective").Handler(
		templateRead(http.HandlerFunc(templateController.GetEffectiveTemplatePermissions))).Methods("GET", "HEAD")
	projectTmplManagement.Path("/{template_id}/stop_all_tasks").Handler(
		templateRun(http.HandlerFunc(taskController.StopAllTasks))).Methods("POST")

	projectTmplManagement.Path("/{template_id}/perms").Handler(
		templateACLManage(http.HandlerFunc(templateController.GetTemplatePerms))).Methods("GET")
	projectTmplManagement.Path("/{template_id}/perms").Handler(
		templateACLManage(http.HandlerFunc(templateController.AddTemplatePerm))).Methods("POST")
	projectTmplManagement.Path("/{template_id}/perms/{perm_id}").Handler(
		templateACLManage(http.HandlerFunc(templateController.GetTemplatePerm))).Methods("GET")
	projectTmplManagement.Path("/{template_id}/perms/{perm_id}").Handler(
		templateACLManage(http.HandlerFunc(templateController.UpdateTemplatePerm))).Methods("PUT")
	projectTmplManagement.Path("/{template_id}/perms/{perm_id}").Handler(
		templateACLManage(http.HandlerFunc(templateController.DeleteTemplatePerm))).Methods("DELETE")

	projectTmplInvManagement := projectTmplManagement.PathPrefix("/{template_id}/inventory").Subrouter()
	projectTmplInvManagement.Use(projects.InventoryMiddleware)
	projectTmplInvManagement.Path("/{inventory_id}/set_default").Handler(
		templateEdit(http.HandlerFunc(projects.SetTemplateInventory))).Methods("POST")
	projectTmplInvManagement.Path("/{inventory_id}/attach").Handler(
		templateEdit(http.HandlerFunc(projects.AttachInventory))).Methods("POST")
	projectTmplInvManagement.Path("/{inventory_id}/detach").Handler(
		templateEdit(http.HandlerFunc(projects.DetachInventory))).Methods("POST")

	workflowView := projects.WorkflowAccessMiddleware(pro_interfaces.PermissionViewWorkflow)
	workflowEdit := projects.WorkflowAccessMiddleware(pro_interfaces.PermissionEditWorkflow)
	workflowStart := projects.WorkflowAccessMiddleware(pro_interfaces.PermissionStartWorkflow)
	workflowStop := projects.WorkflowAccessMiddleware(pro_interfaces.PermissionStopWorkflow)
	workflowAdmin := projects.WorkflowAccessMiddleware(pro_interfaces.PermissionAdministerWorkflow)

	projectWorkflowManagement := authenticatedAPI.PathPrefix("/project/{project_id}/workflows").Subrouter()
	projectWorkflowManagement.Use(projects.ProjectMiddleware, workflowMiddlewareController.WorkflowsMiddleware, workflowAudit)
	projectWorkflowManagement.Handle("/{workflow_id}", workflowEdit(http.HandlerFunc(workflowController.UpdateWorkflow))).Methods("PUT")
	projectWorkflowManagement.Handle("/{workflow_id}", workflowAdmin(http.HandlerFunc(workflowController.RemoveWorkflow))).Methods("DELETE")
	projectWorkflowManagement.Handle("/{workflow_id}", workflowView(http.HandlerFunc(workflowController.GetWorkflow))).Methods("GET", "HEAD")
	projectWorkflowManagement.Handle("/{workflow_id}/versions", workflowView(http.HandlerFunc(workflowController.GetWorkflowVersions))).Methods("GET", "HEAD")
	projectWorkflowManagement.Handle("/{workflow_id}/versions/diff", workflowView(http.HandlerFunc(workflowController.DiffWorkflowVersions))).Methods("GET", "HEAD")
	projectWorkflowManagement.Handle("/{workflow_id}/versions/{version_number}", workflowView(http.HandlerFunc(workflowController.GetWorkflowVersion))).Methods("GET", "HEAD")
	projectWorkflowManagement.Handle("/{workflow_id}/versions/{version_number}/restore", workflowEdit(http.HandlerFunc(workflowController.RestoreWorkflowVersion))).Methods("POST")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers", workflowAdmin(http.HandlerFunc(workflowTriggerController.GetTriggers))).Methods("GET", "HEAD")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers", workflowAdmin(http.HandlerFunc(workflowTriggerController.AddTrigger))).Methods("POST")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers/{trigger_id}", workflowAdmin(http.HandlerFunc(workflowTriggerController.GetTrigger))).Methods("GET", "HEAD")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers/{trigger_id}", workflowAdmin(http.HandlerFunc(workflowTriggerController.UpdateTrigger))).Methods("PUT")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers/{trigger_id}", workflowAdmin(http.HandlerFunc(workflowTriggerController.DeleteTrigger))).Methods("DELETE")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers/{trigger_id}/enabled", workflowAdmin(http.HandlerFunc(workflowTriggerController.SetTriggerEnabled))).Methods("PUT")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers/{trigger_id}/rotate", workflowAdmin(http.HandlerFunc(workflowTriggerController.RotateTriggerCredential))).Methods("POST")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers/{trigger_id}/test", workflowAdmin(http.HandlerFunc(workflowTriggerController.TestTrigger))).Methods("POST")
	projectWorkflowManagement.Handle("/{workflow_id}/triggers/{trigger_id}/history", workflowAdmin(http.HandlerFunc(workflowTriggerController.GetTriggerHistory))).Methods("GET", "HEAD")

	projectWorkflowApprovalInboxAPI := authenticatedAPI.PathPrefix("/project/{project_id}/workflow-approvals").Subrouter()
	projectWorkflowApprovalInboxAPI.Use(projects.ProjectMiddleware, workflowAudit)
	projectWorkflowApprovalInboxAPI.HandleFunc("", workflowController.GetWorkflowApprovalInbox).Methods("GET", "HEAD")

	projectWorkflowRunAPI := authenticatedAPI.PathPrefix("/project/{project_id}/workflows").Subrouter()
	projectWorkflowRunAPI.Use(projects.ProjectMiddleware, workflowMiddlewareController.WorkflowsMiddleware, workflowAudit)
	projectWorkflowRunAPI.Handle("/{workflow_id}/preflight", workflowStart(
		capabilityController.SnapshotMiddleware(
			capabilityController.RequireCapability(
				pro_interfaces.CapabilityExecutionPreflight,
				pro_interfaces.CapabilityAccessRead,
			)(http.HandlerFunc(workflowController.PreviewWorkflow)),
		),
	)).Methods("POST")
	projectWorkflowRunAPI.Handle("/{workflow_id}/run", workflowStart(
		capabilityController.RequireExecutionPreflightForReviewedStart(http.HandlerFunc(workflowController.RunWorkflow)),
	)).Methods("POST")
	projectWorkflowRunAPI.Handle("/{workflow_id}/runs", workflowView(http.HandlerFunc(workflowController.GetWorkflowRuns))).Methods("GET", "HEAD")

	projectWorkflowRunManagement := projectWorkflowRunAPI.PathPrefix("/{workflow_id}/runs").Subrouter()
	projectWorkflowRunManagement.Use(workflowMiddlewareController.WorkflowRunsMiddleware)
	projectWorkflowRunManagement.HandleFunc("/{run_id}", workflowController.GetWorkflowRun).Methods("GET", "HEAD")
	projectWorkflowRunManagement.Handle("/{run_id}/stop", workflowStop(http.HandlerFunc(workflowController.StopWorkflowRun))).Methods("POST")
	projectWorkflowRunManagement.Handle("/{run_id}/retry-reconcile", workflowAdmin(http.HandlerFunc(workflowController.RetryWorkflowRunReconciliation))).Methods("POST")
	projectWorkflowRunManagement.HandleFunc("/{run_id}/artifacts", workflowController.GetWorkflowRunArtifacts).Methods("GET", "HEAD")
	projectWorkflowRunManagement.HandleFunc("/{run_id}/approvals", workflowController.GetWorkflowApprovals).Methods("GET", "HEAD")
	// Approval decisions are authorized again by the workflow service against
	// the immutable request snapshot and current role eligibility. Do not apply
	// ordinary workflow-view narrowing here: an eligible approver may need this
	// bounded decision route without broader workflow visibility.
	projectWorkflowRunManagement.HandleFunc("/{run_id}/approvals/{node_id}", workflowController.ResolveWorkflowApproval).Methods("POST")

	projectTaskManagement := authenticatedAPI.PathPrefix("/project/{project_id}/tasks").Subrouter()
	projectTaskManagement.Use(projects.ProjectMiddleware, taskController.GetTaskMiddleware, taskController.WorkflowTaskAccessMiddleware, workflowAudit)

	projectTaskManagement.HandleFunc("/{task_id}/output", taskController.GetTaskOutput).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/raw_output", taskController.GetTaskRawOutput).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/runner-attempts", taskController.GetTaskRunnerAttempts).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/recovery", taskController.GetTaskRecoveryDiagnostics).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/credential-usage", globalCredentialController.ListTaskUsage).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}", taskController.GetTask).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}", taskController.RemoveTask).Methods("DELETE")
	projectTaskManagement.HandleFunc("/{task_id}/stages", taskController.GetTaskStages).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/ansible/hosts", taskController.GetAnsibleTaskHosts).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/ansible/errors", taskController.GetAnsibleTaskErrors).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/ansible/summary", taskController.GetTaskSummary).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/ansible/summary/hosts", taskController.GetTaskSummaryHosts).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/ansible/summary/stages", taskController.GetTaskSummaryStages).Methods("GET", "HEAD")
	projectTaskManagement.HandleFunc("/{task_id}/ansible/summary/errors", taskController.GetTaskSummaryErrors).Methods("GET", "HEAD")

	projectScheduleManagement := projectUserAPI.PathPrefix("/schedules").Subrouter()
	projectScheduleManagement.Use(projects.SchedulesMiddleware)
	projectScheduleManagement.HandleFunc("/{schedule_id}", projects.GetSchedule).Methods("GET", "HEAD")
	projectScheduleManagement.HandleFunc("/{schedule_id}", projects.UpdateSchedule).Methods("PUT")
	projectScheduleManagement.HandleFunc("/{schedule_id}/active", projects.SetScheduleActive).Methods("PUT")
	projectScheduleManagement.HandleFunc("/{schedule_id}", projects.RemoveSchedule).Methods("DELETE")

	projectViewManagement := projectUserAPI.PathPrefix("/views").Subrouter()
	projectViewManagement.Use(projects.ViewMiddleware)
	projectViewManagement.HandleFunc("/{view_id}", projects.GetViews).Methods("GET", "HEAD")
	projectViewManagement.HandleFunc("/{view_id}", projects.UpdateView).Methods("PUT")
	projectViewManagement.HandleFunc("/{view_id}", projects.RemoveView).Methods("DELETE")
	projectViewManagement.HandleFunc("/{view_id}/templates", projects.GetViewTemplates).Methods("GET", "HEAD")

	projectIntegrationsAliasAPI := projectUserAPI.PathPrefix("/integrations").Subrouter()
	projectIntegrationsAliasAPI.Use(projects.ProjectMiddleware)
	projectIntegrationsAliasAPI.HandleFunc("/aliases", projects.GetIntegrationAlias).Methods("GET", "HEAD")
	projectIntegrationsAliasAPI.HandleFunc("/aliases", projects.AddIntegrationAlias).Methods("POST")
	projectIntegrationsAliasAPI.HandleFunc("/aliases/{alias_id}", projects.RemoveIntegrationAlias).Methods("DELETE")

	projectIntegrationsAPI := projectUserAPI.PathPrefix("/integrations").Subrouter()
	projectIntegrationsAPI.Use(projects.ProjectMiddleware, projects.IntegrationMiddleware)
	projectIntegrationsAPI.HandleFunc("/{integration_id}", projects.UpdateIntegration).Methods("PUT")
	projectIntegrationsAPI.HandleFunc("/{integration_id}", projects.DeleteIntegration).Methods("DELETE")
	projectIntegrationsAPI.HandleFunc("/{integration_id}", projects.GetIntegration).Methods("GET")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/refs", projects.GetIntegrationRefs).Methods("GET", "HEAD")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/matchers", projects.GetIntegrationMatchers).Methods("GET", "HEAD")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/matchers", projects.AddIntegrationMatcher).Methods("POST")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/values", projects.GetIntegrationExtractValues).Methods("GET", "HEAD")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/values", projects.AddIntegrationExtractValue).Methods("POST")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/aliases", projects.GetIntegrationAlias).Methods("GET", "HEAD")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/aliases", projects.AddIntegrationAlias).Methods("POST")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/aliases/{alias_id}", projects.RemoveIntegrationAlias).Methods("DELETE")

	projectIntegrationsAPI.HandleFunc("/{integration_id}/matchers/{matcher_id}", projects.GetIntegrationMatcher).Methods("GET", "HEAD")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/matchers/{matcher_id}", projects.UpdateIntegrationMatcher).Methods("PUT")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/matchers/{matcher_id}", projects.DeleteIntegrationMatcher).Methods("DELETE")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/matchers/{matcher_id}/refs", projects.GetIntegrationMatcherRefs).Methods("GET", "HEAD")

	projectIntegrationsAPI.HandleFunc("/{integration_id}/values/{value_id}", projects.GetIntegrationExtractValue).Methods("GET", "HEAD")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/values/{value_id}", projects.UpdateIntegrationExtractValue).Methods("PUT")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/values/{value_id}", projects.DeleteIntegrationExtractValue).Methods("DELETE")
	projectIntegrationsAPI.HandleFunc("/{integration_id}/values/{value_id}/refs", projects.GetIntegrationExtractValueRefs).Methods("GET")

	if os.Getenv("DEBUG") == "1" {
		defer debugPrintRoutes(r)
	}

	return r
}

func debugPrintRoutes(r *mux.Router) {
	err := r.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		pathTemplate, err := route.GetPathTemplate()
		if err == nil {
			fmt.Println("ROUTE:", pathTemplate)
		}
		pathRegexp, err := route.GetPathRegexp()
		if err == nil {
			fmt.Println("Path regexp:", pathRegexp)
		}
		queriesTemplates, err := route.GetQueriesTemplates()
		if err == nil {
			fmt.Println("Queries templates:", strings.Join(queriesTemplates, ","))
		}
		queriesRegexps, err := route.GetQueriesRegexp()
		if err == nil {
			fmt.Println("Queries regexps:", strings.Join(queriesRegexps, ","))
		}
		methods, err := route.GetMethods()
		if err == nil {
			fmt.Println("Methods:", strings.Join(methods, ","))
		}
		fmt.Println()
		return nil
	})

	if err != nil {
		fmt.Println(err)
	}
}

func servePublic(w http.ResponseWriter, r *http.Request) {
	webPath := "/"
	if util.WebHostURL != nil {
		webPath = util.WebHostURL.Path
		if !strings.HasSuffix(webPath, "/") {
			webPath += "/"
		}
	}

	reqPath := r.URL.Path
	apiPath := path.Join(webPath, "api")

	if reqPath == apiPath || strings.HasPrefix(reqPath, apiPath) {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	// Check if this is a request for the swagger UI
	swaggerPath := path.Join(webPath, "swagger")
	if reqPath == swaggerPath || reqPath == swaggerPath+"/" {
		serveFile(w, r, "swagger/index.html")
		return
	}

	if !strings.Contains(reqPath, ".") {
		serveFile(w, r, "index.html")
		return
	}

	newPath := strings.Replace(
		reqPath,
		webPath,
		"",
		1,
	)

	serveFile(w, r, newPath)
}

func serveFile(w http.ResponseWriter, r *http.Request, name string) {
	res, err := publicAssets.ReadFile(
		fmt.Sprintf("public/%s", name),
	)

	if err != nil {
		http.Error(
			w,
			http.StatusText(http.StatusNotFound),
			http.StatusNotFound,
		)

		return
	}

	if util.WebHostURL != nil && name == "index.html" {
		baseURL := util.WebHostURL.String()

		if !strings.HasSuffix(baseURL, "/") {
			baseURL += "/"
		}

		res = []byte(
			strings.Replace(
				string(res),
				`<base href="/">`,
				fmt.Sprintf(`<base href="%s">`, baseURL),
				1,
			),
		)
	}

	if !strings.HasSuffix(name, ".html") {
		w.Header().Add(
			"Cache-Control",
			fmt.Sprintf("max-age=%d, public, must-revalidate, proxy-revalidate", 24*time.Hour),
		)
	}

	http.ServeContent(
		w,
		r,
		name,
		startTime,
		bytes.NewReader(
			res,
		),
	)
}
