package schedules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"

	"github.com/robfig/cron/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/services/tasks"
	log "github.com/sirupsen/logrus"
)

type ScheduleRunner struct {
	projectID         int
	scheduleID        int
	pool              *SchedulePool
	encryptionService server.AccessKeyEncryptionService
	keyInstaller      db_lib.AccessKeyInstaller
}

// ScheduleOccurrence describes one intended run of a schedule definition.
// Revision is a deterministic digest of every setting that can change the
// work, so an edited schedule cannot reuse an older occurrence claim.
type ScheduleOccurrence struct {
	ScheduleID int
	Revision   string
	IntendedAt time.Time
}

// ScheduleExecutionLease is the scheduler-facing part of the durable HA
// lease. It deliberately exposes fencing checks rather than Redis details.
type ScheduleExecutionLease interface {
	OccurrenceKey() string
	IsCurrent() (bool, error)
	Complete(taskID int) (bool, error)
	Block(decisionID int) (bool, error)
	Release() (bool, error)
}

type oneTimeSchedule struct {
	runAt time.Time
	ran   bool
}

func (s *oneTimeSchedule) Next(t time.Time) time.Time {
	if s.ran {
		return time.Time{}
	}

	if !t.Before(s.runAt) {
		s.ran = true
		return time.Time{}
	}

	return s.runAt
}

func CreateScheduleRunner(
	projectID int,
	scheduleID int,
	pool *SchedulePool,
	encryptionService server.AccessKeyEncryptionService,
	keyInstaller db_lib.AccessKeyInstaller,
) ScheduleRunner {
	return ScheduleRunner{
		projectID:         projectID,
		scheduleID:        scheduleID,
		pool:              pool,
		encryptionService: encryptionService,
		keyInstaller:      keyInstaller,
	}
}

func (r ScheduleRunner) tryUpdateScheduleCommitHash(schedule db.Schedule) (updated bool, err error) {
	repo, err := r.pool.store.GetRepository(schedule.ProjectID, *schedule.RepositoryID)
	if err != nil {
		return
	}

	err = r.pool.encryptionService.DeserializeSecret(&repo.SSHKey)
	if err != nil {
		return
	}

	remoteHash, err := db_lib.GitRepository{
		Logger:     nil,
		TemplateID: schedule.TemplateID,
		Repository: repo,
		Client:     db_lib.CreateDefaultGitClient(r.keyInstaller),
	}.GetLastRemoteCommitHash()

	if err != nil {
		return
	}

	if schedule.LastCommitHash != nil && remoteHash == *schedule.LastCommitHash {
		return
	}

	err = r.pool.store.SetScheduleCommitHash(schedule.ProjectID, schedule.ID, remoteHash)
	if err != nil {
		return
	}

	updated = true
	return
}

func (r ScheduleRunner) Run() {
	schedule, err := r.pool.store.GetSchedule(r.projectID, r.scheduleID)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"context":     common_errors.GetErrorContext(),
			"project_id":  r.projectID,
			"schedule_id": r.scheduleID,
		}).Error("failed to get schedule")
		return
	}

	scheduleType := schedule.Type
	if scheduleType == "" {
		scheduleType = db.ScheduleTypeCron
	}

	if schedule.RepositoryID != nil {
		var updated bool
		updated, err = r.tryUpdateScheduleCommitHash(schedule)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"context":     common_errors.GetErrorContext(),
				"project_id":  r.projectID,
				"schedule_id": r.scheduleID,
			}).Error("failed to update schedule commit hash")
			return
		}
		if !updated {
			return
		}
	}

	tpl, err := r.pool.store.GetTemplate(schedule.ProjectID, schedule.TemplateID)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"context":     common_errors.GetErrorContext(),
			"project_id":  schedule.ProjectID,
			"schedule_id": schedule.ID,
			"template_id": schedule.TemplateID,
		}).Error("failed to get template")
		return
	}

	intendedAt := time.Now().UTC().Truncate(time.Minute)
	if scheduleType == db.ScheduleTypeRunAt && schedule.RunAt != nil {
		intendedAt = schedule.RunAt.UTC()
	}
	occurrence, occurrenceErr := NewScheduleOccurrence(schedule, intendedAt)
	if occurrenceErr != nil {
		log.WithError(occurrenceErr).WithField("schedule_id", schedule.ID).Error("invalid schedule occurrence")
		return
	}
	var lease ScheduleExecutionLease
	if r.pool.dedup != nil {
		claimed, acquired, claimErr := r.pool.dedup.ClaimScheduleOccurrence(occurrence)
		if claimErr != nil {
			log.WithError(claimErr).WithField("schedule_id", schedule.ID).Error("failed to claim schedule occurrence")
			return
		}
		if !acquired {
			log.WithFields(log.Fields{
				"project_id":  schedule.ProjectID,
				"schedule_id": schedule.ID,
				"occurrence":  occurrence.Revision,
			}).Debug("schedule occurrence already claimed by another node")
			if scheduleType == db.ScheduleTypeRunAt {
				r.pool.Refresh()
			}
			return
		}
		lease = claimed
		current, currentErr := lease.IsCurrent()
		if currentErr != nil || !current {
			if currentErr != nil {
				log.WithError(currentErr).WithField("schedule_id", schedule.ID).Error("failed to re-check schedule lease")
			}
			return
		}
	}
	if r.pool.taskPool.DeploymentWindowAdmissionEnabled() && lease == nil {
		log.WithField("schedule_id", schedule.ID).Error("deployment-window schedule admission requires durable occurrence coordination")
		return
	}

	var task db.Task
	if schedule.TaskParams != nil {
		task = schedule.TaskParams.CreateTask(schedule.TemplateID)
	} else {
		task = db.Task{
			ProjectID:  schedule.ProjectID,
			TemplateID: schedule.TemplateID,
		}
	}
	task.ScheduleID = &schedule.ID
	if lease != nil {
		occurrenceKey := lease.OccurrenceKey()
		task.ScheduleOccurrenceKey = &occurrenceKey
	}

	decisionKey := deploymentWindowScheduleDecisionKey(occurrence)
	templateID := schedule.TemplateID
	scheduleID := schedule.ID
	createdTask, err := r.pool.taskPool.AddTaskWithDeploymentWindowAdmission(
		task,
		nil,
		"",
		schedule.ProjectID,
		tpl.App.NeedTaskAlias(),
		pro_interfaces.DeploymentWindowAdmissionRequest{
			ProjectID: schedule.ProjectID, DecisionKey: decisionKey, Source: pro_interfaces.DeploymentWindowSourceSchedule,
			Origin: pro_interfaces.DeploymentWindowOriginSchedule, TemplateID: &templateID, ScheduleID: &scheduleID,
		},
	)

	if err != nil {
		var blocked *pro_interfaces.DeploymentWindowBlockedError
		if errors.As(err, &blocked) {
			if lease == nil || blocked.DecisionID <= 0 {
				log.WithError(err).WithField("schedule_id", schedule.ID).Error("blocked schedule admission has no durable occurrence decision")
				return
			}
			terminal, blockErr := lease.Block(blocked.DecisionID)
			if blockErr != nil || !terminal {
				log.WithError(blockErr).WithFields(log.Fields{"schedule_id": schedule.ID, "decision_id": blocked.DecisionID}).Error("failed to persist blocked schedule occurrence")
			}
			return
		}
		if lease != nil {
			_, _ = lease.Release()
		}
		log.WithError(err).WithFields(log.Fields{
			"context":     common_errors.GetErrorContext(),
			"project_id":  schedule.ProjectID,
			"schedule_id": schedule.ID,
			"template_id": schedule.TemplateID,
		}).Error("failed to add task")
	} else if lease != nil {
		completed, completeErr := lease.Complete(createdTask.ID)
		if completeErr != nil || !completed {
			log.WithError(completeErr).WithFields(log.Fields{
				"schedule_id": schedule.ID,
				"task_id":     createdTask.ID,
			}).Error("failed to complete schedule occurrence")
		}
	}

	// For "RunAt" schedules, the schedule should only trigger once at the specified time and be deactivated afterwards.
	// Calling Refresh here ensures that after the job has fired, the pool reloads the active schedules
	// from the database (where this run-at schedule may now be disabled) so it is not executed again.
	if scheduleType == db.ScheduleTypeRunAt {
		r.pool.Refresh()
	}
}

// deploymentWindowScheduleDecisionKey is stable for an HA replay of one
// schedule occurrence, while retaining the schedule identity. The revision is
// intentionally insufficient by itself because two schedules in one project
// may have identical definitions and therefore identical revisions.
func deploymentWindowScheduleDecisionKey(occurrence ScheduleOccurrence) string {
	durable, err := pro_interfaces.NewScheduleOccurrence(occurrence.ScheduleID, occurrence.Revision, occurrence.IntendedAt)
	if err != nil {
		return ""
	}
	key, err := pro_interfaces.DeploymentWindowScheduleDecisionKey(durable)
	if err != nil {
		return ""
	}
	return key
}

// ScheduleDeduplicator claims one intended schedule fire through durable
// authority. Redis can wake a node, but the implementation must keep
// correctness in SQL and expose a fenced lease for the final re-check.
type ScheduleDeduplicator interface {
	ClaimScheduleOccurrence(occurrence ScheduleOccurrence) (ScheduleExecutionLease, bool, error)
}

// NewScheduleOccurrence derives the relevant revision and normalizes the
// intended fire instant before an HA implementation creates its durable key.
func NewScheduleOccurrence(schedule db.Schedule, intendedAt time.Time) (ScheduleOccurrence, error) {
	if schedule.ID <= 0 || intendedAt.IsZero() {
		return ScheduleOccurrence{}, errors.New("schedule occurrence identity is invalid")
	}
	revisionInput := struct {
		TemplateID     int
		CronFormat     string
		Timezone       *string
		Type           string
		RepositoryID   *int
		RunAt          *time.Time
		DeleteAfterRun bool
		TaskParams     *db.TaskParams
	}{
		TemplateID: schedule.TemplateID, CronFormat: schedule.CronFormat, Timezone: schedule.Timezone, Type: schedule.Type,
		RepositoryID: schedule.RepositoryID, RunAt: schedule.RunAt, DeleteAfterRun: schedule.DeleteAfterRun,
		TaskParams: schedule.TaskParams,
	}
	encoded, err := json.Marshal(revisionInput)
	if err != nil {
		return ScheduleOccurrence{}, err
	}
	digest := sha256.Sum256(encoded)
	return ScheduleOccurrence{
		ScheduleID: schedule.ID,
		Revision:   "sha256:" + hex.EncodeToString(digest[:]),
		IntendedAt: intendedAt.UTC(),
	}, nil
}

type SchedulePool struct {
	cron              *cron.Cron
	locker            sync.Locker
	dedup             ScheduleDeduplicator
	store             db.Store
	taskPool          *tasks.TaskPool
	encryptionService server.AccessKeyEncryptionService
	keyInstaller      db_lib.AccessKeyInstaller
}

// SetDeduplicator configures a distributed schedule deduplicator for HA mode.
// When set, only one node in the cluster fires each schedule occurrence.
func (p *SchedulePool) SetDeduplicator(d ScheduleDeduplicator) {
	p.dedup = d
}

func (p *SchedulePool) init() {
	globalTimezone := defaultScheduleTimezone
	if util.Config.Schedule != nil && util.Config.Schedule.Timezone != "" {
		globalTimezone = util.Config.Schedule.Timezone
	}
	loc, err := time.LoadLocation(globalTimezone)
	if err != nil {
		panic(err)
	}
	p.cron = cron.New(cron.WithLocation(loc))
	p.locker = &sync.Mutex{}
}

func (p *SchedulePool) Refresh() {

	schedules, err := p.store.GetSchedules()

	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"context": common_errors.GetErrorContext(),
		}).Error("failed to get schedules")
		return
	}

	p.locker.Lock()
	defer p.locker.Unlock()

	p.clear()
	now := time.Now().In(p.cron.Location())
	globalTimezone := p.cron.Location().String()
	for _, schedule := range schedules {
		scheduleType := schedule.Type
		if scheduleType == "" {
			scheduleType = db.ScheduleTypeCron
		}

		if schedule.RepositoryID == nil && !schedule.Active {
			continue
		}

		runner := CreateScheduleRunner(
			schedule.ProjectID,
			schedule.ID,
			p,
			p.encryptionService,
			p.keyInstaller,
		)

		switch scheduleType {
		case db.ScheduleTypeRunAt:
			if schedule.RunAt == nil {
				log.WithFields(log.Fields{
					"project_id":  schedule.ProjectID,
					"schedule_id": schedule.ID,
				}).Warn("run_at schedule has no run_at value")
				continue
			}

			runAt := schedule.RunAt.In(p.cron.Location())

			if !runAt.After(now) {
				if schedule.DeleteAfterRun {
					err = p.store.DeleteSchedule(schedule.ProjectID, schedule.ID)
					if err != nil {
						log.WithError(err).WithFields(log.Fields{
							"context":     common_errors.GetErrorContext(),
							"project_id":  schedule.ProjectID,
							"schedule_id": schedule.ID,
						}).Warn("failed to delete past run_at schedule")
					}
				} else if schedule.Active {
					err = p.store.SetScheduleActive(schedule.ProjectID, schedule.ID, false)
					if err != nil {
						log.WithError(err).WithFields(log.Fields{
							"context":     common_errors.GetErrorContext(),
							"project_id":  schedule.ProjectID,
							"schedule_id": schedule.ID,
						}).Warn("failed to deactivate past run_at schedule")
					}
				}
				continue
			}

			_, err = p.addOneTimeRunner(runner, runAt)
		case db.ScheduleTypeCron:
			if schedule.CronFormat == "" {
				continue
			}

			_, expression, resolveErr := resolveScheduleExpression(schedule, globalTimezone)
			if resolveErr != nil {
				err = resolveErr
				break
			}
			_, err = p.addRunner(runner, expression)
		default:
			log.WithFields(log.Fields{
				"project_id":  schedule.ProjectID,
				"schedule_id": schedule.ID,
				"type":        schedule.Type,
			}).Warn("schedule has unsupported type")
			continue
		}

		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"context":     common_errors.GetErrorContext(),
				"project_id":  schedule.ProjectID,
				"schedule_id": schedule.ID,
			}).Errorf("failed to add schedule")
		}
	}
}

func (p *SchedulePool) addRunner(runner ScheduleRunner, cronFormat string) (int, error) {
	id, err := p.cron.AddJob(cronFormat, runner)

	if err != nil {
		return 0, err
	}

	return int(id), nil
}

func (p *SchedulePool) addOneTimeRunner(runner ScheduleRunner, runAt time.Time) (int, error) {
	id := p.cron.Schedule(&oneTimeSchedule{runAt: runAt}, runner)

	return int(id), nil
}

func (p *SchedulePool) Run() {
	p.cron.Run()
}

func (p *SchedulePool) clear() {
	runners := p.cron.Entries()
	for _, r := range runners {
		p.cron.Remove(r.ID)
	}
}

func (p *SchedulePool) Destroy() {
	p.locker.Lock()
	defer p.locker.Unlock()
	p.cron.Stop()
	p.clear()
	p.cron = nil
}

func CreateSchedulePool(
	store db.Store,
	taskPool *tasks.TaskPool,
	keyInstaller db_lib.AccessKeyInstaller,
	encryptionService server.AccessKeyEncryptionService,
) *SchedulePool {
	pool := &SchedulePool{
		store:             store,
		taskPool:          taskPool,
		keyInstaller:      keyInstaller,
		encryptionService: encryptionService,
	}
	pool.init()
	pool.Refresh()
	return pool
}

func ValidateCronFormat(cronFormat string) error {
	_, err := cron.ParseStandard(cronFormat)
	return err
}
