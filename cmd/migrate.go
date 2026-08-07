package cmd

import (
	"context"

	"sumi/config"
	"sumi/internal/database"
	"sumi/pkg/logx"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations and exit",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.NewConfig()
		logx.Configure(cfg.LogFormat)

		ctx, cancel := context.WithTimeout(context.Background(), cfg.MigrationTimeout)
		defer cancel()

		if len(args) > 0 && args[0] == "status" {
			if err := database.MigrationStatus(ctx, cfg); err != nil {
				logx.WithError(err).Fatal("Failed to show database migration status")
			}
			return
		}

		if err := database.RunMigrations(ctx, cfg); err != nil {
			logx.WithError(err).Fatal("Failed to run database migrations")
		}

		logx.Info("Database migrations completed.")
	},
}

func init() {
	rootView.AddCommand(migrateCmd)
}
