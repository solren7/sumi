package services

import (
	"sumi/config"
	"sumi/internal/repository/dbgen"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Deps is the shared infrastructure every service is built from. It is a struct
// rather than a positional parameter list so adding a dependency does not change
// the signature of every constructor and its call sites.
//
// Pool is only needed by services that run multi-statement transactions.
type Deps struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	Config  *config.Config
	Redis   redis.UniversalClient
}

type Service struct {
	Auth     *AuthService
	APIKey   *APIKeyService
	Category *CategoryService
	Bill     *BillService
	Stats    *StatsService
}

func NewService(deps Deps) *Service {
	statsSvc := NewStatsService(deps)

	return &Service{
		Auth:     NewAuthService(deps),
		APIKey:   NewAPIKeyService(deps),
		Category: NewCategoryService(deps),
		Bill:     NewBillService(deps, statsSvc),
		Stats:    statsSvc,
	}
}
