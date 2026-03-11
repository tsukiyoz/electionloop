/*
Copyright © 2026 tsukiyo <tsukiyo6@163.com>
*/
package cmd

import (
	option "zen/cmd/options/runner"
	"zen/internal/runner"
	"zen/pkg/app"

	"github.com/spf13/cobra"
)

// runnerCmd represents the runner command
var runnerCmd = NewRunnerCommand()

func NewRunnerCommand() *cobra.Command {
	sopt := option.NewOptions()

	c := &cobra.Command{
		Use:   "runner",
		Short: "runner",
		Long:  `this is runner`,

		PreRunE: app.PreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := runner.NewConfig(sopt)
			if err != nil {
				return err
			}

			completed, err := cfg.Complete()
			if err != nil {
				return err
			}

			server, err := runner.CreateServer(completed)
			if err != nil {
				return err
			}

			return server.Run(cmd.Context())
		},
	}

	app.SetOption(sopt)
	app.CompleteCommand(c)

	return c
}

func init() {
	rootCmd.AddCommand(runnerCmd)
}
