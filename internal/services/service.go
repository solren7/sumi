package services

import (
	"sumi/config"
	"sumi/internal/repository/dbgen"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	Auth     *AuthService
	APIKey   *APIKeyService
	Category *CategoryService
	Bill     *BillService
	Stats    *StatsService
}

func NewService(pool *pgxpool.Pool, q *dbgen.Queries, cfg *config.Config, rdb redis.UniversalClient) *Service {
	statsSvc := NewStatsService(q, cfg, rdb)

	return &Service{
		Auth:     NewAuthService(pool, q, cfg, rdb),
		APIKey:   NewAPIKeyService(q, cfg, rdb),
		Category: NewCategoryService(q, cfg, rdb),
		Bill:     NewBillService(q, cfg, rdb, statsSvc),
		Stats:    statsSvc,
	}
}
