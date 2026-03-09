/*
Copyright © 2026 tsukiyo <tsukiyo6@163.com>
*/
package cmd

import (
	"log/slog"

	"zen/cmd/options/runner"
	"zen/internal/config"
	"zen/internal/data"
	"zen/internal/server"
	"zen/pkg/app"

	"github.com/spf13/cobra"
)

// runnerCmd represents the runner command
var runnerCmd = NewRunnerCommand()

func NewRunnerCommand() *cobra.Command {
	sopt := runner.NewOptions()

	c := &cobra.Command{
		Use:   "runner",
		Short: "runner",
		Long:  `this is runner`,

		PreRunE: app.PreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.Info("runner called")

			cfg, err := config.NewConfig(sopt)
			if err != nil {
				return err
			}

			data, err := data.NewData(cfg)
			if err != nil {
				return err
			}
			defer data.Close()

			server, err := server.NewServer(cmd.Context(), data)
			if err != nil {
				return err
			}

			return server.Run()
		},
	}

	app.SetOption(sopt)
	app.CompleteCommand(c)

	return c
}

func init() {
	rootCmd.AddCommand(runnerCmd)
}
