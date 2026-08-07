package database

import (
	"context"
	"database/sql"
	"fmt"

	"sumi/config"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func RunMigrations(ctx context.Context, cfg *config.Config) error {
	db, err := sql.Open("pgx", cfg.DBDSN)
	if err != nil {
		return fmt.Errorf("open database for migrations: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database for migrations: %w", err)
	}

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, cfg.MigrationsDir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	return nil
}

func MigrationStatus(ctx context.Context, cfg *config.Config) error {
	db, err := sql.Open("pgx", cfg.DBDSN)
	if err != nil {
		return fmt.Errorf("open database for migration status: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database for migration status: %w", err)
	}

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.StatusContext(ctx, db, cfg.MigrationsDir); err != nil {
		return fmt.Errorf("goose status: %w", err)
	}

	return nil
}
