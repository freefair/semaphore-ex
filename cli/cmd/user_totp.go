package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	for _, cmd := range []*cobra.Command{totpDisableCmd} {
		cmd.PersistentFlags().StringVar(&targetUserArgs.login, "login", "", "User login")
		totpCmd.AddCommand(cmd)
	}
	userCmd.AddCommand(totpCmd)
}

var totpCmd = &cobra.Command{
	Use:   "totp",
	Short: "Manage TOTP verification",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
		os.Exit(0)
	},
}

var totpDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Locally reset TOTP verification and revoke all sessions",
	Run: func(cmd *cobra.Command, args []string) {

		if targetUserArgs.login == "" {
			fmt.Println("Argument --login required")
			os.Exit(1)
		}

		store := createStore("")
		defer store.Close()

		user, err := store.GetUserByLoginOrEmail(targetUserArgs.login, "")

		if err != nil {
			panic(err)
		}

		if user.Totp == nil {
			fmt.Println("TOTP not enabled")
			os.Exit(1)
		}

		err = store.ForceResetTOTPEnrollmentAndRevokeSessions(user.ID, user.Totp.ID)
		if err != nil {
			panic(err)
		}
		fmt.Println("TOTP disabled and all sessions revoked for", user.Username)
	},
}
