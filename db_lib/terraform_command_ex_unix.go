//go:build !windows

package db_lib

import (
	"os"
	"os/exec"
)

func prepareTerraformCommand(_ *exec.Cmd) {}

func interruptTerraformCommand(cmd *exec.Cmd) error {
	// Signal only the CLI: it coordinates provider shutdown itself. Terragrunt
	// forwards the interrupt to Terraform; signaling both would interrupt twice.
	return cmd.Process.Signal(os.Interrupt)
}
