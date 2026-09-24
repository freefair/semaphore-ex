//go:build !windows

package db_lib

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type inventoryProcessIDs struct {
	inventory  int
	grandchild int
}

func TestInventoryResolverStopDuringPopenReturnKillsSeparateSession(t *testing.T) {
	python, err := exec.LookPath("python3")
	require.NoError(t, err)
	root := t.TempDir()
	resolver, err := installInventoryResolver(root)
	require.NoError(t, err)
	harness := filepath.Join(root, "popen-harness.py")
	require.NoError(t, os.WriteFile(harness, []byte(inventoryResolverPopenHarness), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ansible-inventory"), []byte(inventoryResolverBlockingInventory(python)), 0o700))

	pids := filepath.Join(root, "inventory-pids")
	ready := filepath.Join(root, "popen-ready")
	release := filepath.Join(root, "popen-release")
	require.NoError(t, syscall.Mkfifo(release, 0o600))
	command := exec.Command(python, harness, resolver, "refresh", "site.yml")
	command.Env = append(os.Environ(),
		"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PID_MARKER="+pids,
		"HARNESS_READY="+ready,
		"HARNESS_RELEASE="+release,
	)
	require.NoError(t, command.Start())
	processes := inventoryProcessIDs{}
	t.Cleanup(func() {
		killInventoryProcesses(processes)
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})

	require.Eventually(t, func() bool {
		content, readErr := os.ReadFile(pids)
		if readErr != nil {
			return false
		}
		parts := strings.Fields(string(content))
		if len(parts) != 2 {
			return false
		}
		processes.inventory, readErr = strconv.Atoi(parts[0])
		if readErr != nil {
			return false
		}
		processes.grandchild, readErr = strconv.Atoi(parts[1])
		return readErr == nil && processes.inventory > 0 && processes.grandchild > 0
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(ready)
		return statErr == nil
	}, time.Second, 10*time.Millisecond)

	// The harness is now inside the resolver's Popen call after its child has
	// started but before Popen can return to the resolver. TERM must stay pending
	// until the resolver installs its child-killing handler.
	require.NoError(t, syscall.Kill(command.Process.Pid, syscall.SIGTERM))
	releasePipe, err := os.OpenFile(release, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	require.NoError(t, err, "the protected resolver must still be waiting in Popen")
	_, err = releasePipe.Write([]byte("r"))
	require.NoError(t, err)
	require.NoError(t, releasePipe.Close())
	require.Error(t, command.Wait())

	require.Eventually(t, func() bool { return processStopped(processes.inventory) }, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return processStopped(processes.grandchild) }, time.Second, 10*time.Millisecond)
}

func killInventoryProcesses(ids inventoryProcessIDs) {
	for _, pid := range []int{ids.inventory, ids.grandchild} {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func processStopped(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return true
	}
	closing := strings.LastIndex(string(status), ")")
	return closing >= 0 && len(status) > closing+2 && status[closing+2] == 'Z'
}

const inventoryResolverPopenHarness = `
import os
import runpy
import subprocess
import sys
import time

resolver = sys.argv[1]
original_popen = subprocess.Popen

def held_popen(*args, **kwargs):
    process = original_popen(*args, **kwargs)
    deadline = time.monotonic() + 1
    while not os.path.exists(os.environ["PID_MARKER"]):
        if time.monotonic() >= deadline:
            raise RuntimeError("inventory PID marker was not written")
        time.sleep(0.005)
    with open(os.environ["HARNESS_RELEASE"], "r+b", buffering=0) as control:
        with open(os.environ["HARNESS_READY"], "w") as marker:
            marker.write("ready")
        control.read(1)
    return process

subprocess.Popen = held_popen
sys.argv = [resolver] + sys.argv[2:]
runpy.run_path(resolver, run_name="__main__")
`

func inventoryResolverBlockingInventory(python string) string {
	return "#!" + python + `
import os
import signal
import subprocess
import sys
import time

blocked = signal.pthread_sigmask(signal.SIG_BLOCK, [])
assert signal.SIGTERM not in blocked
grandchild = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
with open(os.environ["PID_MARKER"], "w") as marker:
    marker.write("%d %d" % (os.getpid(), grandchild.pid))
while True:
    time.sleep(1)
`
}
