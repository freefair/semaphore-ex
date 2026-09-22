//go:build !windows

package db_lib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func terraformLockingFixture(t *testing.T, name, script string) (*TerraformApp, string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "terraform-fixture")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"+script), 0o700))
	previous := util.Config
	util.Config = &util.ConfigType{Apps: map[string]util.App{name: {AppPath: binary}}, TmpPath: dir, Process: &util.ConfigProcess{}}
	t.Cleanup(func() { util.Config = previous })
	app := &TerraformApp{Name: name, Template: db.Template{ID: 1}, Repository: db.Repository{GitURL: dir}}
	app.SetLogger(task_logger.NopLogger{})
	app.reader.EOF = true
	return app, dir
}

func TestTerraformStagesPreserveDefaultLocking(t *testing.T) {
	for _, name := range []string{"terraform", "tofu", "terragrunt"} {
		for _, stage := range []string{"init", "plan", "apply"} {
			for _, explicitOverride := range []bool{false, true} {
				t.Run(name+"/"+stage+"/override="+map[bool]string{true: "yes", false: "no"}[explicitOverride], func(t *testing.T) {
					app, dir := terraformLockingFixture(t, name, `printf '%s\n' "$@" > args`)
					args := []string{"-lock-timeout=30s"}
					if explicitOverride {
						args = append(args, "-lock=false")
					}
					var err error
					switch stage {
					case "init":
						err = app.init([]string{"SSH_AUTH_SOCK=/fixture-agent"}, &terraformAgentInstaller{}, &db.TerraformTaskParams{}, args)
					case "plan":
						err = app.Plan(args, nil, nil, nil)
					case "apply":
						err = app.Apply(args, nil, nil, nil)
					}
					require.NoError(t, err)
					data, err := os.ReadFile(filepath.Join(dir, "args"))
					require.NoError(t, err)
					actual := strings.Fields(string(data))
					assert.Equal(t, stage, actual[0])
					assert.Contains(t, actual, "-lock-timeout=30s")
					assert.Equal(t, map[bool]int{true: 1, false: 0}[explicitOverride], strings.Count(string(data), "-lock=false"))
				})
			}
		}
	}
}

func TestTerraformCancellationWaitsForUnlock(t *testing.T) {
	app, dir := terraformLockingFixture(t, "terraform", `
trap 'printf "interrupt\n" >> signals; touch stopping' INT
trap 'printf "term\n" >> signals; exit 43' TERM
touch state.lock ready
while [ ! -e release ]; do sleep 0.02; done
rm state.lock
exit 0
`)
	stop := make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- app.Apply(nil, nil, nil, stop) }()
	// Always release our fixture, including when testing the broken baseline.
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600) })
	require.Eventually(t, func() bool { return testFileExists(filepath.Join(dir, "ready")) }, 5*time.Second, 10*time.Millisecond)
	close(stop)
	require.Eventually(t, func() bool { return testFileExists(filepath.Join(dir, "stopping")) }, 3*time.Second, 10*time.Millisecond)
	select {
	case err := <-result:
		require.FailNow(t, "Terraform exited before state persistence/unlock", "%v", err)
	case <-time.After(16 * time.Second):
	}
	assert.FileExists(t, filepath.Join(dir, "state.lock"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0o600))
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Terraform did not finish after releasing its lock")
	}
	assert.NoFileExists(t, filepath.Join(dir, "state.lock"))
	signals, err := os.ReadFile(filepath.Join(dir, "signals"))
	require.NoError(t, err)
	assert.Equal(t, "interrupt\n", string(signals))
}

func TestTerraformCancellationPreventsFollowingStages(t *testing.T) {
	for _, stage := range []string{"init", "plan"} {
		t.Run(stage, func(t *testing.T) {
			app, dir := terraformLockingFixture(t, "terraform", `
printf '%s\n' "$1" >> stages
trap 'touch interrupted; exit 0' INT
trap 'exit 43' TERM
touch ready
while [ ! -e release ]; do sleep 0.02; done
`)
			stop := make(chan struct{})
			result := make(chan error, 1)
			if stage == "init" {
				// No success/error output and no confirmation response: cancellation
				// must unblock init's stdin reader without a log-parser callback.
				app.reader.EOF = false
			}
			go func() {
				if stage == "init" {
					result <- app.InstallRequirementsWithInitArgs(LocalAppInstallingArgs{
						EnvironmentVars: []string{"SSH_AUTH_SOCK=/fixture-agent"},
						Installer:       &terraformAgentInstaller{}, Params: &db.TerraformTaskParams{},
						TplParams: &db.TerraformTemplateParams{}, StopCh: stop,
					}, nil)
				} else {
					result <- app.Run(LocalAppRunningArgs{StopCh: stop,
						TaskParams: &db.TerraformTaskParams{}, TemplateParams: &db.TerraformTemplateParams{AutoApprove: true}})
				}
			}()
			t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600) })
			require.Eventually(t, func() bool { return testFileExists(filepath.Join(dir, "ready")) }, 5*time.Second, 10*time.Millisecond)
			close(stop)
			select {
			case err := <-result:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "cancelled Terraform stage did not finish")
			}
			assert.FileExists(t, filepath.Join(dir, "interrupted"))
			stages, err := os.ReadFile(filepath.Join(dir, "stages"))
			require.NoError(t, err)
			assert.Equal(t, stage+"\n", string(stages), "cancellation must not advance to workspace selection or apply")
		})
	}
}

func TestTerraformPendingCancellationStartsNoProcess(t *testing.T) {
	app, dir := terraformLockingFixture(t, "terraform", `touch started`)
	stop := make(chan struct{})
	close(stop)
	require.NoError(t, app.Plan(nil, nil, nil, stop))
	require.NoError(t, app.Apply(nil, nil, nil, stop))
	require.NoError(t, app.InstallRequirementsWithInitArgs(LocalAppInstallingArgs{
		EnvironmentVars: []string{"SSH_AUTH_SOCK=/fixture-agent"},
		Installer:       &terraformAgentInstaller{}, Params: &db.TerraformTaskParams{},
		TplParams: &db.TerraformTemplateParams{}, StopCh: stop,
	}, nil))
	assert.NoFileExists(t, filepath.Join(dir, "started"))
}
