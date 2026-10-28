package cmd

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/spf13/cobra"
)

const infisicalPAMConnectCommandName = "infisical-pam-connect"

type infisicalPAMConnectArgs struct {
	socket string
	host   string
}

func newInfisicalPAMConnectCommand(stdin io.Reader, stdout io.Writer) *cobra.Command {
	args := &infisicalPAMConnectArgs{}
	command := &cobra.Command{
		Use:    infisicalPAMConnectCommandName,
		Short:  "Tunnel one SSH connection through the task's Infisical PAM broker",
		Hidden: true,
		Args:   cobra.NoArgs,
		// stdout carries the SSH stream: the root hook's log-level notice
		// would corrupt it.
		PersistentPreRun: func(*cobra.Command, []string) {},
		SilenceUsage:     true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := infisicalpam.Connect(ctx, args.socket, args.host, stdin, stdout); err != nil {
				return fmt.Errorf("Infisical PAM: connection to %s failed: %s", args.host, infisicalPAMCategory(err))
			}
			return nil
		},
	}
	command.Flags().StringVar(&args.socket, "socket", "", "Task broker socket")
	command.Flags().StringVar(&args.host, "host", "", "SSH host name to connect to")
	_ = command.MarkFlagRequired("socket")
	_ = command.MarkFlagRequired("host")
	return command
}

func infisicalPAMCategory(err error) string {
	if category := infisicalpam.CategoryOf(err); category != "" {
		return string(category)
	}
	return "transport"
}

func init() {
	rootCmd.AddCommand(newInfisicalPAMConnectCommand(os.Stdin, os.Stdout))
}
