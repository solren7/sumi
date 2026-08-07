package database

import (
	"context"
	"sumi/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.DBDSN)
	if err != nil {
		return nil, err
	}

	// 测试连接是否可用
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}

	return pool, nil
}
