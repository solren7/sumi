package services

import (
	"context"
	"encoding/json"

	"sumi/config"
	"sumi/internal/cache"
	"sumi/internal/repository/dbgen"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
)

type CategoryService struct {
	q   *dbgen.Queries
	cfg *config.Config
	rdb redis.UniversalClient
}

type CategoryNode struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	Type     int16          `json:"type"`
	Level    int16          `json:"level"`
	ParentID *int64         `json:"parent_id,omitempty"`
	Children []CategoryNode `json:"children,omitempty"`
}

func NewCategoryService(q *dbgen.Queries, cfg *config.Config, rdb redis.UniversalClient) *CategoryService {
	return &CategoryService{q: q, cfg: cfg, rdb: rdb}
}

func (s *CategoryService) ListCategoriesByUser(ctx context.Context, userID uuid.UUID, categoryType int16) ([]CategoryNode, error) {
	cacheKey := cache.UserCategoriesKey(userID, categoryType)
	var cached []CategoryNode
	if raw, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil && json.Unmarshal([]byte(raw), &cached) == nil {
		return cached, nil
	}

	rows, err := s.q.ListCategoriesByUserAndType(ctx, dbgen.ListCategoriesByUserAndTypeParams{
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		Type:   categoryType,
	})
	if err != nil {
		return nil, err
	}

	tree := buildCategoryTree(rows)
	payload, err := json.Marshal(tree)
	if err == nil {
		_ = s.rdb.Set(ctx, cacheKey, payload, s.cfg.CategoryCacheTTL).Err()
	}

	return tree, nil
}

func buildCategoryTree(categories []dbgen.Category) []CategoryNode {
	roots := make([]CategoryNode, 0)
	nodes := make(map[int64]*CategoryNode, len(categories))

	for _, category := range categories {
		node := CategoryNode{
			ID:       category.ID,
			Name:     category.Name,
			Type:     category.Type,
			Level:    category.Level,
			ParentID: category.ParentID,
			Children: []CategoryNode{},
		}
		nodes[category.ID] = &node
	}

	for _, category := range categories {
		node := nodes[category.ID]
		if category.ParentID == nil {
			roots = append(roots, *node)
			continue
		}

		parent := nodes[*category.ParentID]
		if parent == nil {
			continue
		}
		parent.Children = append(parent.Children, *node)
	}

	for i := range roots {
		if root := nodes[roots[i].ID]; root != nil {
			roots[i] = *root
		}
	}

	return roots
}
