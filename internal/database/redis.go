package database

import (
	"context"
	"fmt"

	"sumi/config" // 替换为你实际的 config 包路径

	"github.com/redis/go-redis/v9"
)

func NewRedis(ctx context.Context, cfg *config.Config) (redis.UniversalClient, error) {
	opt, _ := redis.ParseURL(cfg.RedisConfig.RedisURL)
	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return rdb, nil
}
