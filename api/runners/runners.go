package runners

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/jwt"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/runners"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

func RunnerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		token := r.Header.Get("X-Runner-Token")

		if token == "" {
			helpers.WriteJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "Invalid token",
			})
			return
		}

		store := helpers.Store(r)

		runner, err := store.GetRunnerByToken(token)

		if err != nil {
			helpers.WriteJSON(w, http.StatusNotFound, map[string]string{
				"error": "Runner not found",
			})
			return
		}

		if runner.Token != token {
			helpers.WriteJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "Invalid token",
			})
			return
		}

		r = helpers.SetContextValue(r, "runner", runner)
		next.ServeHTTP(w, r)
	})
}

type RunnerController struct {
	runnerRepo                db.RunnerManager
	taskPool                  *tasks.TaskPool
	encryptionService         server.AccessKeyEncryptionService
	signer                    jwt.Signer
	taskExecutionEvidenceSink db.TaskExecutionEvidenceRecorder
	metrics                   *metrics.Metrics
}

// SetMetrics is additive so existing runner-controller call sites retain their
// evidence-sink contract while the API router wires the process metrics once.
func (c *RunnerController) SetMetrics(appMetrics *metrics.Metrics) { c.metrics = appMetrics }

func NewRunnerController(runnerRepo db.RunnerManager, taskPool *tasks.TaskPool, encryptionService server.AccessKeyEncryptionService, signer jwt.Signer, evidenceSinks ...db.TaskExecutionEvidenceRecorder) *RunnerController {
	controller := &RunnerController{
		runnerRepo:        runnerRepo,
		taskPool:          taskPool,
		encryptionService: encryptionService,
		signer:            signer,
	}
	if len(evidenceSinks) > 0 {
		controller.taskExecutionEvidenceSink = evidenceSinks[0]
	}
	return controller
}

func (c *RunnerController) GetRunner(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(db.Runner)

	clearCache := false
	report, err := runners.ParseHealthReport(r.Header)
	if err != nil {
		helpers.WriteErrorStatus(w, "Invalid runner health report", http.StatusBadRequest)
		return
	}
	report.Apply(&runner)
	securityReport := db.RunnerSecurityReport{
		RegistrationKind: runner.RegistrationKind,
		PublicKey:        "",
	}
	if runner.PublicKey != nil {
		securityReport.PublicKey = *runner.PublicKey
	}
	if report.Version != nil {
		securityReport.RunnerVersion = *report.Version
	}
	if report.ExecutorType != nil {
		securityReport.ExecutorType = *report.ExecutorType
	}
	if report.TransportTrust != nil {
		securityReport.TransportTrust = *report.TransportTrust
	}
	if report.SecurityProtocolVersion != nil {
		securityReport.ProtocolVersion = *report.SecurityProtocolVersion
	}
	securityDecision := db.EvaluateRunnerRegistrationPolicy(runner.RegistrationPolicy, securityReport)
	runner.SecurityCompliant = securityDecision.Compliant
	runner.SecurityReason = securityDecision.Reason
	runner.SecurityRemediation = securityDecision.Remediation
	checkedAt := time.Now().UTC()
	runner.SecurityCheckedAt = &checkedAt
	if !securityDecision.Compliant {
		if updateErr := c.runnerRepo.UpdateRunnerSecurity(runner); updateErr != nil {
			log.WithError(updateErr).WithField("runner_id", runner.ID).Error("failed to persist runner security decision")
		}
		helpers.WriteJSON(w, http.StatusConflict, securityDecision)
		return
	}

	// The runner reports its process start time on every poll. It changes on
	// every restart and is persisted next to "touched", so the task reconciler
	// can detect runners that restarted and lost their in-memory job pool.
	if v := r.Header.Get("X-Runner-Started-At"); v != "" {
		if startedAt, parseErr := time.Parse(time.RFC3339, v); parseErr == nil {
			runner.StartedAt = &startedAt
		} else {
			log.WithFields(log.Fields{
				"runner_id": runner.ID,
				"context":   "runner",
			}).WithError(parseErr).Warn("invalid X-Runner-Started-At header")
		}
	}

	if err := c.runnerRepo.TouchRunner(runner); err != nil {
		log.WithFields(log.Fields{
			"runner_id": runner.ID,
			"context":   "runner",
		}).WithError(err).Error("runner touch failed")
		helpers.WriteError(w, err)
		return
	}

	if runner.IsCacheClearPending() {
		clearCache = true
	}

	data := runners.RunnerState{
		RunnerID:   runner.ID,
		AccessKeys: make(map[int]db.AccessKey),
		ClearCache: clearCache,
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorDocker {
		sessionStore, ok := c.runnerRepo.(db.DockerReconciliationSessionRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Docker reconciliation storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		session, sessionErr := sessionStore.OpenDockerReconciliationSession(runner.ID, r.Header.Get(runners.RunnerDockerSessionHeader), r.Header.Get(runners.RunnerDockerFenceHeader))
		if sessionErr != nil {
			helpers.WriteError(w, sessionErr)
			return
		}
		policyStore, ok := c.runnerRepo.(db.DockerExecutionPolicyRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Docker policy storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		policy, policyErr := policyStore.GetDockerExecutionPolicy()
		if policyErr != nil {
			helpers.WriteError(w, policyErr)
			return
		}
		data.DockerPolicy = &policy
		data.DockerReconciliationSession = &session
		if remediationStore, remediationOK := c.runnerRepo.(db.DockerReconciliationRepository); remediationOK {
			commands, commandErr := remediationStore.GetDockerReconciliationRemediationCommands(runner.ID, session.SessionID, session.Fence, 100)
			if commandErr != nil {
				helpers.WriteError(w, commandErr)
				return
			}
			data.DockerReconciliationCommands = commands
		}
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorK8s {
		policyStore, ok := c.runnerRepo.(db.KubernetesExecutionPolicyRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Kubernetes policy storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		if runner.K8sClusterAlias == "" {
			helpers.WriteErrorStatus(w, "Kubernetes runner cluster alias is unavailable", http.StatusConflict)
			return
		}
		policy, policyErr := policyStore.GetKubernetesExecutionPolicy(runner.K8sClusterAlias)
		if policyErr != nil {
			helpers.WriteError(w, policyErr)
			return
		}
		data.KubernetesPolicy = &policy
		if runner.K8sNamespace == "" || !kubernetesNamespaceAllowed(policy, runner.K8sNamespace) {
			helpers.WriteErrorStatus(w, "Kubernetes runner namespace is not permitted by policy", http.StatusConflict)
			return
		}
		sessionStore, ok := c.runnerRepo.(db.KubernetesReconciliationSessionRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Kubernetes reconciliation storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		session, sessionErr := sessionStore.OpenKubernetesReconciliationSession(runner.ID, runner.K8sClusterAlias, runner.K8sNamespace, r.Header.Get(runners.RunnerKubernetesSessionHeader), r.Header.Get(runners.RunnerKubernetesFenceHeader))
		if sessionErr != nil {
			helpers.WriteError(w, sessionErr)
			return
		}
		data.KubernetesReconciliationSession = &session
		if commands, commandsErr := sessionStore.GetKubernetesReconciliationCommands(runner.ID, session.SessionID, session.Fence, 100); commandsErr == nil {
			data.KubernetesReconciliationCommands = commands
		}
	}

	if clearCache {
		data.CacheCleanProjectID = runner.ProjectID
	}

	runningTasks := c.taskPool.GetRunningTasks()

	for _, tsk := range runningTasks {
		if tsk.Task.RunnerID == nil || *tsk.Task.RunnerID != runner.ID {
			continue
		}
		if tsk.Task.Status == task_logger.TaskWaitingStatus || tsk.Task.Status == task_logger.TaskStartingStatus {

			c.prepareRemoteJob(tsk, &runner, &data)

		} else {
			data.CurrentJobs = append(data.CurrentJobs, runners.JobState{
				ID:         tsk.Task.ID,
				Generation: tsk.Task.AssignmentGeneration,
				Status:     tsk.Task.Status,
			})
		}
	}

	helpers.WriteJSON(w, http.StatusOK, data)
}

// prepareRemoteJob builds the job for a waiting/starting task and stages it,
// together with the task's decrypted access keys, into data. On any failure it
// fails and finalizes the task in place, leaving data untouched, so a single
// bad task does not abort the poll for the whole runner.
func (c *RunnerController) prepareRemoteJob(tsk *tasks.TaskRunner, runner *db.Runner, data *runners.RunnerState) {
	if runner.EffectiveExecutorType() == db.RunnerExecutorDocker {
		if data.DockerReconciliationSession == nil || !data.DockerReconciliationSession.Ready {
			return
		}
		policyStore, ok := c.runnerRepo.(db.DockerExecutionPolicyRepository)
		if !ok {
			return
		}
		policy, policyErr := policyStore.GetDockerExecutionPolicy()
		if policyErr != nil || !policy.MatchesAck(db.DockerExecutionPolicyAck{Revision: runner.DockerPolicyRevision, Hash: runner.DockerPolicyHash}) {
			return
		}
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorK8s {
		if data.KubernetesReconciliationSession == nil || !data.KubernetesReconciliationSession.Ready {
			return
		}
		policyStore, ok := c.runnerRepo.(db.KubernetesExecutionPolicyRepository)
		if !ok || runner.K8sClusterAlias == "" {
			return
		}
		policy, policyErr := policyStore.GetKubernetesExecutionPolicy(runner.K8sClusterAlias)
		if policyErr != nil || !policy.MatchesAck(db.KubernetesExecutionPolicyAck{ClusterAlias: runner.K8sClusterAlias, Revision: runner.K8sPolicyRevision, Hash: runner.K8sPolicyHash}) {
			return
		}
	}
	if tsk.Task.ResolvedExecutorImage != nil && !runner.SupportsExecutorImage() {
		tsk.Log("Runner executor does not support the resolved image. Use a Docker or Kubernetes runner, or clear the template executor image.")
		tsk.SetStatus(task_logger.TaskFailStatus)
		c.taskPool.FinalizeRemoteTask(tsk, runner)
		return
	}
	// Survey secret variables are stored as a task-bound encrypted
	// access key in the shared DB, so any HA node serving this poll can
	// deliver them. An unreadable (e.g. expired) secret fails the task
	// instead of dispatching it with silently empty variables.
	surveySecrets, err := c.encryptionService.GetTaskSurveySecrets(tsk.Task.ProjectID, tsk.Task.ID)
	if err != nil {
		logger := log.WithError(err).WithFields(log.Fields{
			"runner_id": runner.ID,
			"task_id":   tsk.Task.ID,
			"context":   "survey_secrets",
		})
		if errors.Is(err, server.ErrAccessKeyExpired) {
			logger.Warn("task survey secrets expired before dispatch")
			tsk.Log("Survey secrets expired before the task started. Please run the task again.")
		} else {
			logger.Error("failed to read task survey secrets")
			tsk.Log("Failed to read survey secrets. More details in the server logs.")
		}
		tsk.SetStatus(task_logger.TaskFailStatus)
		c.taskPool.FinalizeRemoteTask(tsk, runner)
		return
	}

	jobData := runners.JobData{
		Username:            tsk.Username,
		IncomingVersion:     tsk.IncomingVersion,
		Alias:               tsk.Alias,
		Task:                tsk.Task,
		Template:            tsk.Template,
		Inventory:           tsk.Inventory,
		InventoryRepository: tsk.Inventory.Repository,
		Repository:          tsk.Repository,
		Environment:         tsk.Environment,
		ExecutorImage:       tsk.Task.ResolvedExecutorImage,
	}
	jobData.Template.ExecutorImage = jobData.ExecutorImage

	// Always overwrite: the dispatched Secret must be exactly the
	// DB-derived value, never whatever the in-memory task carries
	jobData.Task.Secret = surveySecrets

	if c.signer != nil && tsk.Template.JWTParams != nil && tsk.Template.JWTParams.Enabled {
		ttl, terr := tsk.Template.JWTParams.ParsedTTL()
		if terr != nil {
			log.WithError(terr).WithFields(log.Fields{
				"task_id":     tsk.Task.ID,
				"template_id": tsk.Template.ID,
				"context":     "jwt",
			}).Warn("invalid template jwt_params.ttl")
			tsk.Log("Invalid JWT token lifetime in the template settings: " + terr.Error())
			tsk.SetStatus(task_logger.TaskFailStatus)
			c.taskPool.FinalizeRemoteTask(tsk, runner)
			return
		}

		token, jerr := c.signer.Sign(jwt.TaskInfo{
			TaskID:     tsk.Task.ID,
			ProjectID:  tsk.Task.ProjectID,
			TemplateID: tsk.Template.ID,
			UserID:     tsk.Task.UserID,
			Audience:   tsk.Template.JWTParams.Audience,
			TTL:        ttl,
		})
		if jerr != nil {
			log.WithError(jerr).WithFields(log.Fields{
				"task_id": tsk.Task.ID,
				"context": "jwt",
			}).Error("failed to sign task JWT")
			tsk.Log("Failed to sign the task JWT. More details in the server logs.")
			tsk.SetStatus(task_logger.TaskFailStatus)
			c.taskPool.FinalizeRemoteTask(tsk, runner)
			return
		}

		jobData.JWT = token
	}

	// Decrypt all keys of the task before publishing anything, so a
	// task with an undecryptable key fails on its own instead of
	// aborting the poll for the whole runner, and none of its keys
	// leak into the response of a job that is not dispatched.
	taskKeys := make(map[int]db.AccessKey)
	if kerr := c.collectTaskAccessKeys(tsk, runner.ID, taskKeys); kerr != nil {
		tsk.Log("Failed to decrypt access keys of the task. More details in the server logs.")
		tsk.SetStatus(task_logger.TaskFailStatus)
		c.taskPool.FinalizeRemoteTask(tsk, runner)
		return
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorDocker {
		sessionStore, ok := c.runnerRepo.(db.DockerReconciliationSessionRepository)
		baseName := fmt.Sprintf("semaphore-task-%d-g%d-%s", tsk.Task.ID, tsk.Task.AssignmentGeneration, data.DockerReconciliationSession.TargetBoot)
		targets := []db.DockerReconciliationScanTarget{
			{TargetBoot: data.DockerReconciliationSession.TargetBoot, ProjectID: tsk.Task.ProjectID, TaskID: tsk.Task.ID, Generation: tsk.Task.AssignmentGeneration, Resource: db.DockerReconciliationResourceTask, ContainerName: baseName},
			{TargetBoot: data.DockerReconciliationSession.TargetBoot, ProjectID: tsk.Task.ProjectID, TaskID: tsk.Task.ID, Generation: tsk.Task.AssignmentGeneration, Resource: db.DockerReconciliationResourceHelper, ContainerName: baseName + "-helper"},
		}
		if !ok || sessionStore.BindDockerReconciliationAttempts(runner.ID, *data.DockerReconciliationSession, targets) != nil {
			return
		}
	}

	maps.Copy(data.AccessKeys, taskKeys)
	data.NewJobs = append(data.NewJobs, jobData)
}

func kubernetesNamespaceAllowed(policy db.KubernetesExecutionPolicy, namespace string) bool {
	for _, allowed := range policy.AllowedNamespaces {
		if allowed == namespace {
			return true
		}
	}
	return false
}

// collectTaskAccessKeys decrypts every access key the dispatched task needs
// and stages them into keys, so the caller publishes either all of them or
// none. It returns the first decryption error.
func (c *RunnerController) collectTaskAccessKeys(tsk *tasks.TaskRunner, runnerID int, keys map[int]db.AccessKey) error {
	if tsk.Inventory.SSHKeyID != nil {
		if err := c.encryptionService.DeserializeSecret(&tsk.Inventory.SSHKey); err != nil {
			log.WithFields(log.Fields{
				"runner_id":     runnerID,
				"task_id":       tsk.Task.ID,
				"inventory_id":  tsk.Inventory.ID,
				"access_key_id": tsk.Inventory.SSHKey.ID,
				"context":       "runner",
			}).WithError(err).Error("Failed to decrypt inventory key")
			return err
		}
		keys[*tsk.Inventory.SSHKeyID] = tsk.Inventory.SSHKey
	}

	if tsk.Inventory.BecomeKeyID != nil {
		if err := c.encryptionService.DeserializeSecret(&tsk.Inventory.BecomeKey); err != nil {
			log.WithFields(log.Fields{
				"runner_id":     runnerID,
				"task_id":       tsk.Task.ID,
				"inventory_id":  tsk.Inventory.ID,
				"access_key_id": tsk.Inventory.BecomeKey.ID,
				"context":       "runner",
			}).WithError(err).Error("Failed to decrypt become key")
			return err
		}
		keys[*tsk.Inventory.BecomeKeyID] = tsk.Inventory.BecomeKey
	}

	for _, vault := range tsk.Template.Vaults {
		if vault.VaultKeyID == nil {
			continue
		}
		if err := c.encryptionService.DeserializeSecret(vault.Vault); err != nil {
			log.WithFields(log.Fields{
				"runner_id":     runnerID,
				"task_id":       tsk.Task.ID,
				"access_key_id": vault.Vault.ID,
				"context":       "runner",
			}).WithError(err).Error("Failed to decrypt vault")
			return err
		}
		keys[*vault.VaultKeyID] = *vault.Vault
	}

	if tsk.Inventory.RepositoryID != nil {
		if err := c.encryptionService.DeserializeSecret(&tsk.Inventory.Repository.SSHKey); err != nil {
			log.WithFields(log.Fields{
				"runner_id":     runnerID,
				"task_id":       tsk.Task.ID,
				"repository_id": tsk.Inventory.Repository.ID,
				"access_key_id": tsk.Inventory.Repository.SSHKey.ID,
				"context":       "runner",
			}).WithError(err).Error("Failed to decrypt repository key")
			return err
		}
		keys[tsk.Inventory.Repository.SSHKeyID] = tsk.Inventory.Repository.SSHKey
	}

	// Decrypt the task repository key here rather than relying on it having
	// been decrypted earlier (e.g. in TaskRunner.populateDetails). Decryption
	// reads key.Secret (the ciphertext, left intact) and refills the plaintext
	// field, so this is idempotent even if the key is already decrypted.
	if err := c.encryptionService.DeserializeSecret(&tsk.Repository.SSHKey); err != nil {
		log.WithFields(log.Fields{
			"runner_id":     runnerID,
			"task_id":       tsk.Task.ID,
			"repository_id": tsk.Repository.ID,
			"access_key_id": tsk.Repository.SSHKey.ID,
			"context":       "runner",
		}).WithError(err).Error("Failed to decrypt repository key")
		return err
	}
	keys[tsk.Repository.SSHKeyID] = tsk.Repository.SSHKey

	return nil
}

func (c *RunnerController) UpdateRunner(w http.ResponseWriter, r *http.Request) {

	runner := helpers.GetFromContext(r, "runner").(db.Runner)

	var body runners.RunnerProgress

	if !helpers.Bind(w, r, &body) {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid format",
		})
		return
	}
	var response runners.RunnerProgressResponse
	if body.DockerTelemetry != nil {
		if runner.EffectiveExecutorType() != db.RunnerExecutorDocker {
			helpers.WriteErrorStatus(w, "Docker telemetry is not available for this runner", http.StatusBadRequest)
			return
		}
		telemetryStore, ok := c.runnerRepo.(db.DockerTelemetryRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Docker telemetry storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		ack, accepted, telemetryErr := telemetryStore.IngestDockerTelemetry(runner.ID, r.Header.Get(runners.RunnerDockerSessionHeader), r.Header.Get(runners.RunnerDockerFenceHeader), *body.DockerTelemetry)
		if telemetryErr != nil {
			if errors.Is(telemetryErr, db.ErrDockerTelemetrySessionStale) || errors.Is(telemetryErr, db.ErrDockerTelemetrySequenceConflict) {
				helpers.WriteErrorStatus(w, "Docker telemetry rejected", http.StatusConflict)
			} else {
				helpers.WriteErrorStatus(w, "Invalid Docker telemetry", http.StatusBadRequest)
			}
			return
		}
		response.DockerTelemetryAck = &ack
		for _, event := range accepted {
			c.metrics.RecordDockerTelemetry(event)
		}
	}
	if body.KubernetesTelemetry != nil {
		if runner.EffectiveExecutorType() != db.RunnerExecutorK8s {
			helpers.WriteErrorStatus(w, "Kubernetes telemetry is not available for this runner", http.StatusBadRequest)
			return
		}
		telemetryStore, ok := c.runnerRepo.(db.KubernetesTelemetryRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Kubernetes telemetry storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		ack, accepted, telemetryErr := telemetryStore.IngestKubernetesTelemetry(runner.ID, r.Header.Get(runners.RunnerKubernetesSessionHeader), r.Header.Get(runners.RunnerKubernetesFenceHeader), *body.KubernetesTelemetry)
		if telemetryErr != nil {
			if errors.Is(telemetryErr, db.ErrKubernetesTelemetrySessionStale) || errors.Is(telemetryErr, db.ErrKubernetesTelemetrySequenceConflict) {
				helpers.WriteErrorStatus(w, "Kubernetes telemetry rejected", http.StatusConflict)
			} else {
				helpers.WriteErrorStatus(w, "Invalid Kubernetes telemetry", http.StatusBadRequest)
			}
			return
		}
		response.KubernetesTelemetryAck = &ack
		for _, event := range accepted {
			c.metrics.RecordKubernetesTelemetry(event)
		}
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorDocker && (len(body.DockerReconciliationObservations) > 0 || body.DockerReconciliationScanComplete != nil || len(body.DockerReconciliationOrphanCandidates) > 0 || len(body.DockerReconciliationQuarantines) > 0) {
		sessionStore, ok := c.runnerRepo.(db.DockerReconciliationSessionRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Docker reconciliation storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		if body.DockerReconciliationScanComplete != nil {
			complete := *body.DockerReconciliationScanComplete
			complete.SessionID, complete.Fence = r.Header.Get(runners.RunnerDockerSessionHeader), r.Header.Get(runners.RunnerDockerFenceHeader)
			if err := sessionStore.IngestDockerReconciliationScan(runner.ID, db.DockerReconciliationScan{SessionID: complete.SessionID, Fence: complete.Fence, Observations: body.DockerReconciliationObservations, Complete: complete}); err != nil {
				helpers.WriteErrorStatus(w, "Docker reconciliation scan rejected", http.StatusConflict)
				return
			}
		} else if len(body.DockerReconciliationObservations) > 0 {
			helpers.WriteErrorStatus(w, "Docker reconciliation scan boundary is required", http.StatusBadRequest)
			return
		}
		if len(body.DockerReconciliationOrphanCandidates) > 0 {
			if err := sessionStore.RecordDockerReconciliationOrphanCandidates(runner.ID, r.Header.Get(runners.RunnerDockerSessionHeader), r.Header.Get(runners.RunnerDockerFenceHeader), body.DockerReconciliationOrphanCandidates); err != nil {
				helpers.WriteErrorStatus(w, "Docker reconciliation orphan candidates rejected", http.StatusConflict)
				return
			}
		}
		for _, quarantine := range body.DockerReconciliationQuarantines {
			if err := sessionStore.QuarantineDockerReconciliationAttempt(runner.ID, r.Header.Get(runners.RunnerDockerSessionHeader), r.Header.Get(runners.RunnerDockerFenceHeader), quarantine); err != nil {
				helpers.WriteErrorStatus(w, "Docker reconciliation quarantine rejected", http.StatusConflict)
				return
			}
		}
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorDocker && len(body.DockerReconciliationRemediationResults) > 0 {
		remediationStore, ok := c.runnerRepo.(db.DockerReconciliationRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Docker reconciliation storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, result := range body.DockerReconciliationRemediationResults {
			if err := remediationStore.ReportDockerReconciliationRemediation(runner.ID, r.Header.Get(runners.RunnerDockerSessionHeader), r.Header.Get(runners.RunnerDockerFenceHeader), result); err != nil {
				helpers.WriteErrorStatus(w, "Docker reconciliation remediation result rejected", http.StatusConflict)
				return
			}
		}
	}
	if body.KubernetesReconciliationScan != nil {
		if runner.EffectiveExecutorType() != db.RunnerExecutorK8s {
			helpers.WriteErrorStatus(w, "Kubernetes reconciliation is not available for this runner", http.StatusBadRequest)
			return
		}
		store, ok := c.runnerRepo.(db.KubernetesReconciliationSessionRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Kubernetes reconciliation storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		scan := *body.KubernetesReconciliationScan
		scan.SessionID, scan.Fence = r.Header.Get(runners.RunnerKubernetesSessionHeader), r.Header.Get(runners.RunnerKubernetesFenceHeader)
		if err := store.IngestKubernetesReconciliationScan(runner.ID, scan); err != nil {
			helpers.WriteErrorStatus(w, "Kubernetes reconciliation scan rejected", http.StatusConflict)
			return
		}
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorK8s && len(body.KubernetesReconciliationRemediationResults) > 0 {
		store, ok := c.runnerRepo.(db.KubernetesReconciliationSessionRepository)
		if !ok {
			helpers.WriteErrorStatus(w, "Kubernetes reconciliation storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		if len(body.KubernetesReconciliationRemediationResults) > 100 {
			helpers.WriteErrorStatus(w, "Kubernetes reconciliation remediation result limit exceeded", http.StatusBadRequest)
			return
		}
		for _, result := range body.KubernetesReconciliationRemediationResults {
			if err := store.ReportKubernetesReconciliationRemediation(runner.ID, r.Header.Get(runners.RunnerKubernetesSessionHeader), r.Header.Get(runners.RunnerKubernetesFenceHeader), result); err != nil {
				helpers.WriteErrorStatus(w, "Kubernetes reconciliation remediation result rejected", http.StatusConflict)
				return
			}
		}
	}

	taskPool := c.taskPool

	var executionEvidence []db.TaskExecutionEvidence
	var err error
	if body.KnownJobs != nil {
		executionEvidence, err = taskExecutionEvidenceFromSnapshot(body.KnownJobs)
		if err != nil {
			helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if runner.EffectiveExecutorType() == db.RunnerExecutorDocker {
		if remediationStore, ok := c.runnerRepo.(db.DockerReconciliationRepository); ok {
			commands, commandErr := remediationStore.GetDockerReconciliationRemediationCommands(runner.ID, r.Header.Get(runners.RunnerDockerSessionHeader), r.Header.Get(runners.RunnerDockerFenceHeader), 100)
			if commandErr == nil {
				response.DockerReconciliationCommands = commands
			}
		}
	}
	if runner.EffectiveExecutorType() == db.RunnerExecutorK8s {
		if store, ok := c.runnerRepo.(db.KubernetesReconciliationSessionRepository); ok {
			if commands, commandErr := store.GetKubernetesReconciliationCommands(runner.ID, r.Header.Get(runners.RunnerKubernetesSessionHeader), r.Header.Get(runners.RunnerKubernetesFenceHeader), 100); commandErr == nil {
				response.KubernetesReconciliationCommands = commands
			}
		}
	}

	if body.Jobs == nil {
		if body.KnownJobs != nil && !c.persistTaskExecutionEvidence(w, runner.ID, executionEvidence) {
			return
		}
		if len(response.DockerReconciliationCommands) > 0 || len(response.KubernetesReconciliationCommands) > 0 || response.DockerTelemetryAck != nil || response.KubernetesTelemetryAck != nil {
			helpers.WriteJSON(w, http.StatusOK, response)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	for _, job := range body.Jobs {
		tsk, err := taskPool.GetTask(job.ID)

		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				// The task no longer exists at all — the runner's job is orphaned.
				response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
				continue
			}
			log.WithError(err).WithFields(log.Fields{
				"task_id":   job.ID,
				"runner_id": runner.ID,
				"context":   "runner",
			}).Warn("runner progress: task not in local pool and could not be loaded from database")
			continue
		}

		if tsk == nil {
			// Not in the pool. The task may have been stopped and finalized while
			// the runner was offline; check the persisted row so the runner can be
			// told to emergency-stop instead of running the job forever.
			row, rowErr := helpers.Store(r).GetTaskByID(job.ID)
			switch {
			case rowErr != nil && errors.Is(rowErr, db.ErrNotFound):
				// The task no longer exists at all — the runner's job is orphaned.
				response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
			case rowErr == nil && row.Status.IsFinished():
				response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
			case rowErr != nil:
				log.WithError(rowErr).WithFields(log.Fields{
					"task_id":   job.ID,
					"runner_id": runner.ID,
					"context":   "runner",
				}).Warn("runner progress: task not in pool and could not be loaded from database")
			default:
				log.WithFields(log.Fields{
					"task_id":   job.ID,
					"runner_id": runner.ID,
					"context":   "runner",
				}).Warn("runner progress: task not found in pool")
			}
			continue
		}

		if tsk.Task.RunnerID == nil || *tsk.Task.RunnerID != runner.ID {
			// The task was reassigned (e.g. reconciler requeued it off an offline
			// runner). Tell this runner to emergency-stop the job instead of
			// rejecting the whole progress batch with 400 — sendProgress treats
			// >=400 as total failure and never applies terminated_jobs, so the
			// old runner would keep executing alongside the new assignee.
			response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
			continue
		}
		reportedGeneration := normalizeReportedGeneration(
			job.Generation, tsk.Task.AssignmentGeneration,
		)
		if reportedGeneration != tsk.Task.AssignmentGeneration {
			response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
			continue
		}

		if !job.Status.IsValid() {
			helpers.WriteErrorStatus(w, "Invalid task status", http.StatusBadRequest)
			return
		}

		// The task already reached a terminal status on the server (e.g. force
		// stopped while the runner was offline, or failed by the reconciler).
		// Reject the report — logs included — and tell the runner to
		// emergency-stop the job.
		if tsk.Task.Status.IsFinished() {
			response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
			continue
		}
		if job.ExecutorMetadata != nil {
			if err := job.ExecutorMetadata.Validate(runner.EffectiveExecutorType()); err != nil {
				helpers.WriteErrorStatus(w, "Invalid executor metadata", http.StatusBadRequest)
				return
			}
			updated, err := helpers.Store(r).UpdateTaskRunnerAttemptMetadata(
				tsk.Task.ProjectID, job.ID, reportedGeneration, runner.ID, *job.ExecutorMetadata,
			)
			if err != nil {
				log.WithError(err).WithFields(log.Fields{
					"task_id": job.ID, "runner_id": runner.ID, "context": "runner_executor_metadata",
				}).Error("failed to persist runner executor metadata")
				helpers.WriteErrorStatus(w, "Failed to persist executor metadata", http.StatusInternalServerError)
				return
			}
			if !updated {
				response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
				continue
			}
		}

		var commitHash *string
		var commitMessage string
		if job.Commit != nil {
			commitHash = &job.Commit.Hash
			commitMessage = job.Commit.Message
		}
		// A graceful stop can race with an in-flight progress snapshot that was
		// captured before the runner received the server's stopping state. Keep
		// the server-owned stopping state and accept the snapshot so the runner
		// remains tracked long enough to report its terminal stopped status while
		// still persisting commit metadata carried by the snapshot.
		inflightCancellationProgress := tsk.Task.Status == task_logger.TaskStoppingStatus &&
			!job.Status.IsFinished()
		acceptedStatus := job.Status
		if inflightCancellationProgress {
			acceptedStatus = task_logger.TaskStoppingStatus
		}
		if !tsk.ApplyRunnerProgress(
			acceptedStatus, runner.ID, reportedGeneration, commitHash, commitMessage,
		) {
			response.TerminatedJobs = append(response.TerminatedJobs, job.ID)
			continue
		}

		for _, logRecord := range job.LogRecords {
			tsk.LogWithTime(logRecord.Time, logRecord.Message)
		}

		// When the runner reports a terminal status, finalize the task here:
		// finish webhook, autorun children, and pool/Redis state cleanup.
		// This is what completes a task independently of the node that
		// originally dispatched it.
		if tsk.Task.Status.IsFinished() {
			runner := runner
			go taskPool.FinalizeRemoteTask(tsk, &runner)
		}
	}
	if body.KnownJobs != nil && !c.persistTaskExecutionEvidence(w, runner.ID, executionEvidence) {
		return
	}

	helpers.WriteJSON(w, http.StatusOK, response)
}

func (c *RunnerController) persistTaskExecutionEvidence(w http.ResponseWriter, runnerID int, evidence []db.TaskExecutionEvidence) bool {
	if c.taskExecutionEvidenceSink == nil {
		return true
	}
	if err := c.taskExecutionEvidenceSink.RecordTaskExecutionSnapshot(runnerID, evidence); err != nil {
		log.WithError(err).WithField("runner_id", runnerID).Error("failed to persist runner execution evidence")
		helpers.WriteErrorStatus(w, "Failed to persist runner execution evidence", http.StatusInternalServerError)
		return false
	}
	return true
}

func taskExecutionEvidenceFromSnapshot(snapshot []runners.JobState) ([]db.TaskExecutionEvidence, error) {
	evidence := make([]db.TaskExecutionEvidence, 0, len(snapshot))
	seen := make(map[[2]int]struct{}, len(snapshot))
	for _, job := range snapshot {
		if job.ID <= 0 || job.Generation <= 0 || !job.Status.IsValid() {
			return nil, errors.New("Invalid runner execution snapshot")
		}
		key := [2]int{job.ID, job.Generation}
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("Invalid runner execution snapshot")
		}
		seen[key] = struct{}{}
		state := db.TaskExecutionEvidenceRunning
		if job.Status.IsFinished() {
			state = db.TaskExecutionEvidenceTerminal
		}
		evidence = append(evidence, db.TaskExecutionEvidence{
			TaskID: job.ID, Generation: job.Generation, State: state, Status: job.Status,
		})
	}
	return evidence, nil
}

// normalizeReportedGeneration keeps rolling upgrades compatible without
// weakening reassignment safety. Generation-unaware runners may finish only a
// task's first assignment; any replacement assignment is generation 2 or newer.
func normalizeReportedGeneration(reported int, current int) int {
	if reported == 0 && current == 1 {
		return 1
	}
	return reported
}

func RegisterRunner(w http.ResponseWriter, r *http.Request) {
	var register runners.RunnerRegistration

	if !helpers.Bind(w, r, &register) {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid format",
		})
		return
	}

	if register.RegistrationToken == "" {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid registration token",
		})
		return
	}
	reportedExecutorType := register.ExecutorType
	executorType, executorErr := db.NormalizeRunnerExecutorType(register.ExecutorType)
	if executorErr != nil {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": executorErr.Error()})
		return
	}
	register.ExecutorType = executorType

	store := helpers.Store(r)

	var runner db.Runner
	var err error

	if strings.HasPrefix(register.RegistrationToken, "smrs_") {
		// Otherwise the value is a one-time registration token issued for a specific
		// unregistered runner. The global token cannot be used to register it.
		runner, err = store.RegisterRunner(server.HashRunnerRegistrationToken(register.RegistrationToken), db.RunnerSecurityReport{
			RegistrationKind: db.RunnerRegistrationOneTime,
			TransportTrust:   register.TransportTrust,
			RunnerVersion:    register.RunnerVersion,
			ProtocolVersion:  register.SecurityProtocolVersion,
			ExecutorType:     reportedExecutorType,
			PublicKey:        register.PublicKey,
		})

		if err != nil {
			var violation db.RunnerSecurityViolationError
			if errors.As(err, &violation) {
				helpers.WriteJSON(w, http.StatusConflict, violation.Decision)
				return
			}
			helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Invalid registration token",
			})
			return
		}
	} else if util.Config.GetRunnerRegistrationToken() != "" && register.RegistrationToken == util.Config.GetRunnerRegistrationToken() {
		// The shared, global registration token creates a brand-new runner.
		runner, err = store.CreateRunner(db.Runner{
			Token:            db.GenerateRunnerToken(),
			Webhook:          register.Webhook,
			Name:             register.Name,
			Tags:             register.Tags,
			MaxParallelTasks: register.MaxParallelTasks,
			Active:           register.Enabled,
			ProjectID:        register.ProjectID,
			ExecutorType:     register.ExecutorType,
		})

		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"context": "runner",
			}).Error("Can't create runner")

			helpers.WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Unexpected error",
			})
			return
		}
	} else {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid registration token",
		})
		return
	}

	log.WithFields(log.Fields{
		"runner_id": runner.ID,
		"context":   "runner",
	}).Info("New runner registered")

	var res struct {
		Token              string                      `json:"token"`
		RegistrationPolicy db.RunnerRegistrationPolicy `json:"registration_policy"`
		SecurityCompliant  bool                        `json:"security_compliant"`
		SecurityReason     string                      `json:"security_reason"`
	}

	res.Token = runner.Token
	res.RegistrationPolicy = runner.RegistrationPolicy
	res.SecurityCompliant = runner.SecurityCompliant
	res.SecurityReason = runner.SecurityReason

	helpers.WriteJSON(w, http.StatusOK, res)
}

func UnregisterRunner(w http.ResponseWriter, r *http.Request) {

	runner := helpers.GetFromContext(r, "runner").(db.Runner)

	err := helpers.Store(r).DeleteGlobalRunner(runner.ID)

	if err != nil {
		helpers.WriteJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Unknown error",
		})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
