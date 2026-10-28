package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfisicalPAMConnectKeepsStdoutFreeOfParentOutput(t *testing.T) {
	stdout := &bytes.Buffer{}
	parent := &cobra.Command{
		Use: "semaphore",
		PersistentPreRun: func(*cobra.Command, []string) {
			_, _ = fmt.Fprintln(stdout, "Log level set to debug")
		},
	}
	parent.AddCommand(newInfisicalPAMConnectCommand(strings.NewReader(""), stdout))
	parent.SetErr(&bytes.Buffer{})
	parent.SetArgs([]string{"infisical-pam-connect", "--socket", filepath.Join(t.TempDir(), "missing.sock"), "--host", "web-1"})

	err := parent.Execute()

	require.Error(t, err)
	assert.Equal(t, "Infisical PAM: connection to web-1 failed: transport", err.Error())
	assert.Empty(t, stdout.String())
}

func TestInfisicalPAMConnectRejectsInvalidHost(t *testing.T) {
	command := newInfisicalPAMConnectCommand(strings.NewReader(""), &bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"--socket", filepath.Join(os.TempDir(), "x.sock"), "--host", "web;id"})

	err := command.Execute()

	assert.EqualError(t, err, "Infisical PAM: connection to web;id failed: validation")
}

func TestInfisicalPAMConnectIsHiddenAndRequiresFlags(t *testing.T) {
	command := newInfisicalPAMConnectCommand(strings.NewReader(""), &bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{})

	assert.True(t, command.Hidden)
	assert.ErrorContains(t, command.Execute(), "required flag")
}
