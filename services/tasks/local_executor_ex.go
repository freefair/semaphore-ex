package tasks

import (
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro/pkg/stage_parsers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"path"
)

func (t *LocalExecutor) prepare(username string, incomingVersion *string, alias string, installRequirements bool) (err error) {
	if t.prepared {
		return nil
	}

	t.SetStatus(task_logger.TaskRunningStatus) // It is required for local mode. Don't delete

	// Defense in depth: reject playbook paths pointing outside the repository
	// even if they were stored before validation was added.
	if err = db.ValidatePlaybookPath(t.Template.Playbook, "template"); err != nil {
		t.Log(err.Error())
		return
	}
	if err = db.ValidatePlaybookPath(t.Task.Playbook, "task"); err != nil {
		t.Log(err.Error())
		return
	}

	environmentVariables, err := t.getEnvironmentENV()
	if err != nil {
		return
	}

	surveyEnvVars, err := t.getSurveyEnvVars()
	if err != nil {
		return
	}
	environmentVariables = append(environmentVariables, surveyEnvVars...)
	if t.Template.App == db.AppAnsible && proFeatures.Compatibility().Edition == pro_interfaces.EditionEnhanced {
		callbackEnvironment, callbackErr := stage_parsers.TaskSummaryCallbackEnvironment(
			path.Join(t.Repository.GetInternalPath(t.Template.ID), "callbacks"),
			environmentVariables,
		)
		if callbackErr != nil {
			t.Log(stage_parsers.TaskSummaryCollectionFailureOutput(callbackErr))
		} else {
			environmentVariables = callbackEnvironment
		}
	}

	tplParams, err := t.getTemplateParams()
	if err != nil {
		return
	}

	params, err := t.getParams()
	if err != nil {
		return
	}

	if t.Template.App.IsTerraform() && alias != "" {
		environmentVariables = append(environmentVariables, "TF_HTTP_ADDRESS="+util.GetPublicAliasURL("terraform", alias))
	}

	// For Terraform apps, get args first so we can pass init args to prepareRun
	var argsMap map[string][]string
	var inputs map[string]string

	if t.Template.App.IsTerraform() {
		argsMap, err = t.getTerraformArgs(username, incomingVersion)
		if err != nil {
			return
		}
		// Use Terraform-specific prepareRun with init args
		if tfApp, ok := t.App.(*db_lib.TerraformApp); ok {
			initArgs := []string(nil)
			if argsMap != nil {
				if stageArgs, ok := argsMap["init"]; ok {
					initArgs = stageArgs
				}
			}

			err = t.prepareRunTerraform(tfApp, db_lib.LocalAppInstallingArgs{
				EnvironmentVars: environmentVariables,
				TplParams:       tplParams,
				Params:          params,
				Installer:       t.KeyInstaller,
			}, initArgs, installRequirements)
			if err != nil {
				return err
			}
		} else {
			err = t.prepareRun(db_lib.LocalAppInstallingArgs{
				EnvironmentVars: environmentVariables,
				TplParams:       tplParams,
				Params:          params,
				Installer:       t.KeyInstaller,
			}, installRequirements)
			if err != nil {
				return err
			}
		}
	} else {
		err = t.prepareRun(db_lib.LocalAppInstallingArgs{
			EnvironmentVars: environmentVariables,
			TplParams:       tplParams,
			Params:          params,
			Installer:       t.KeyInstaller,
		}, installRequirements)
		if err != nil {
			return err
		}
	}

	// Get args for non-Terraform apps
	var args []string
	switch t.Template.App {
	case db.AppAnsible:
		args, inputs, err = t.getPlaybookArgs(username, incomingVersion)
		if err != nil {
			return
		}
		// Convert to map format with "default" key
		argsMap = map[string][]string{"default": args}
	case db.AppTerraform, db.AppTofu, db.AppTerragrunt:
		// Already got args earlier for Terraform
	default:
		args, err = t.getShellArgs(username, incomingVersion)
		if err != nil {
			return
		}
		// Convert to map format with "default" key
		argsMap = map[string][]string{"default": args}
	}

	// Get extra environment vars for non-Terraform apps
	switch t.Template.App {
	case db.AppAnsible:
		// Semaphore vars / task details were already passed
		// as 'extra vars' in JSON format
		break
	case db.AppTerraform, db.AppTofu, db.AppTerragrunt:
		break
	default:
		environmentVariables = append(environmentVariables, t.getShellEnvironmentExtraENV(username, incomingVersion)...)
	}

	if sshEnv := t.getSSHAgentEnv(); sshEnv != "" {
		environmentVariables = append(environmentVariables, sshEnv)
	}

	if t.Template.Type != db.TemplateTask {

		environmentVariables = append(environmentVariables, fmt.Sprintf("SEMAPHORE_TASK_TYPE=%s", t.Template.Type))

		if incomingVersion != nil {
			environmentVariables = append(
				environmentVariables,
				fmt.Sprintf("SEMAPHORE_TASK_INCOMING_VERSION=%s", *incomingVersion))
		}

		if t.Template.Type == db.TemplateBuild && t.Task.Version != nil {
			environmentVariables = append(
				environmentVariables,
				fmt.Sprintf("SEMAPHORE_TASK_TARGET_VERSION=%s", *t.Task.Version))
		}
	}

	t.preparedEnv = environmentVariables
	t.preparedArgsMap = argsMap
	t.preparedInputs = inputs
	t.preparedParams = params
	t.preparedTplParams = tplParams
	t.prepared = true

	return nil
}
