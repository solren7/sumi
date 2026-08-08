package services

import (
	"context"
	"encoding/json"
	"strings"

	"sumi/config"
	"sumi/internal/cache"
	"sumi/internal/domain"
	"sumi/internal/repository/dbgen"
	"sumi/pkg/errorx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
)

type CategoryService struct {
	q   *dbgen.Queries
	cfg *config.Config
	rdb redis.UniversalClient
}

func NewCategoryService(deps Deps) *CategoryService {
	return &CategoryService{q: deps.Queries, cfg: deps.Config, rdb: deps.Redis}
}

func (s *CategoryService) ListCategoriesByUser(ctx context.Context, userID uuid.UUID, categoryType int16) ([]domain.CategoryNode, error) {
	cacheKey := cache.UserCategoriesKey(userID, categoryType)
	var cached []domain.CategoryNode
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

type CreateCategoryInput struct {
	Name       string
	Type       int16
	ParentID   int64
	ParentName string
}

// CreateCategory adds a second-level category under an existing top-level one.
// Bills may only reference second-level categories, so first-level creation is
// deliberately not supported.
//
// The name must be unique among all second-level categories of the same type,
// even across different parents: bills can be created by category name, and a
// duplicated name would make that lookup ambiguous.
func (s *CategoryService) CreateCategory(ctx context.Context, userID uuid.UUID, input CreateCategoryInput) (*domain.CategoryNode, error) {
	name, err := domain.ValidateCategoryName(input.Name)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateBillType(input.Type); err != nil {
		return nil, err
	}

	parentName := strings.TrimSpace(input.ParentName)
	if input.ParentID <= 0 && parentName == "" {
		return nil, errorx.Newf(400, "Either parent_id or parent_name is required; list top-level categories via GET /api/categories?type=%d", input.Type)
	}

	rows, err := s.q.ListCategoriesByUserAndType(ctx, dbgen.ListCategoriesByUserAndTypeParams{
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		Type:   input.Type,
	})
	if err != nil {
		return nil, err
	}

	var parent *dbgen.Category
	for i, category := range rows {
		if category.Level != 1 {
			continue
		}
		if input.ParentID > 0 {
			if category.ID == input.ParentID {
				parent = &rows[i]
				break
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(category.Name), parentName) {
			if parent != nil {
				return nil, errorx.Newf(400, "Parent name %q is ambiguous, use parent_id instead", parentName)
			}
			parent = &rows[i]
		}
	}
	if parent == nil {
		return nil, errorx.Newf(400, "Top-level category not found for this type; list valid parents via GET /api/categories?type=%d", input.Type)
	}

	siblings := 0
	for _, category := range rows {
		if category.Level != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(category.Name), name) {
			return nil, errorx.Newf(409, "Category %q already exists for this type", name)
		}
		if category.ParentID != nil && *category.ParentID == parent.ID {
			siblings++
		}
	}

	created, err := s.q.CreateCategory(ctx, dbgen.CreateCategoryParams{
		UserID:    pgtype.UUID{Bytes: userID, Valid: true},
		Type:      input.Type,
		Name:      name,
		ParentID:  &parent.ID,
		Level:     2,
		SortOrder: int32(siblings + 1),
		Icon:      nil,
		IsSystem:  false,
		IsActive:  true,
	})
	if err != nil {
		return nil, err
	}

	s.invalidateUserCategories(ctx, userID, input.Type)

	return &domain.CategoryNode{
		ID:       created.ID,
		Name:     created.Name,
		Type:     created.Type,
		Level:    created.Level,
		ParentID: created.ParentID,
	}, nil
}

func (s *CategoryService) invalidateUserCategories(ctx context.Context, userID uuid.UUID, categoryType int16) {
	_ = s.rdb.Del(ctx, cache.UserCategoriesKey(userID, categoryType)).Err()
}

func buildCategoryTree(categories []dbgen.Category) []domain.CategoryNode {
	roots := make([]domain.CategoryNode, 0)
	nodes := make(map[int64]*domain.CategoryNode, len(categories))

	for _, category := range categories {
		node := domain.CategoryNode{
			ID:       category.ID,
			Name:     category.Name,
			Type:     category.Type,
			Level:    category.Level,
			ParentID: category.ParentID,
			Children: []domain.CategoryNode{},
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
