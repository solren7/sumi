package services

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"sumi/config"
	"sumi/internal/cache"
	"sumi/internal/repository/dbgen"
	"sumi/pkg/errorx"
	"sumi/pkg/utils"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
)

const apiKeyPrefixLen = 16

type apiKeyCacheValue struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	KeyHash   string     `json:"key_hash"`
	Scopes    []string   `json:"scopes"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type APIKeyIdentity struct {
	APIKeyID uuid.UUID
	UserID   uuid.UUID
	Scopes   []string
}

type CreateAPIKeyInput struct {
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

type CreatedAPIKey struct {
	Record *dbgen.ApiKey
	Key    string
}

type APIKeyService struct {
	q   *dbgen.Queries
	cfg *config.Config
	rdb redis.UniversalClient
}

func NewAPIKeyService(q *dbgen.Queries, cfg *config.Config, rdb redis.UniversalClient) *APIKeyService {
	return &APIKeyService{q: q, cfg: cfg, rdb: rdb}
}

func (s *APIKeyService) Create(ctx context.Context, userID uuid.UUID, input CreateAPIKeyInput) (*CreatedAPIKey, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, errorx.New(400, "API key name is required")
	}

	raw, err := utils.GenerateOpaqueToken(32)
	if err != nil {
		return nil, err
	}

	fullKey := "sk_live_" + raw
	prefix := fullKey
	if len(prefix) > apiKeyPrefixLen {
		prefix = prefix[:apiKeyPrefixLen]
	}

	expiresAt := pgtype.Timestamptz{}
	if input.ExpiresAt != nil {
		expiresAt = pgtype.Timestamptz{Time: *input.ExpiresAt, Valid: true}
	}

	record, err := s.q.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{
		UserID:    userID,
		Name:      name,
		KeyPrefix: prefix,
		KeyHash:   utils.HashSecret(fullKey, s.cfg.APIKeyPepper),
		Scopes:    normalizeScopes(input.Scopes),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, err
	}

	_ = s.cacheAPIKey(ctx, record)
	return &CreatedAPIKey{
		Record: &record,
		Key:    fullKey,
	}, nil
}

func (s *APIKeyService) List(ctx context.Context, userID uuid.UUID) ([]dbgen.ApiKey, error) {
	items, err := s.q.ListAPIKeysByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return []dbgen.ApiKey{}, nil
	}
	return items, nil
}

func (s *APIKeyService) Revoke(ctx context.Context, userID, apiKeyID uuid.UUID) error {
	record, err := s.q.GetAPIKeyByID(ctx, dbgen.GetAPIKeyByIDParams{
		ID:     apiKeyID,
		UserID: userID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return errorx.ErrNotFound
		}
		return err
	}

	if err := s.q.RevokeAPIKey(ctx, dbgen.RevokeAPIKeyParams{
		ID:     apiKeyID,
		UserID: userID,
	}); err != nil {
		return err
	}

	_ = s.rdb.Del(ctx, cache.APIKeyKey(record.KeyPrefix)).Err()
	return nil
}

func (s *APIKeyService) Validate(ctx context.Context, apiKey string, requiredScopes []string) (*APIKeyIdentity, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errorx.ErrUnauthorized
	}

	prefix := apiKey
	if len(prefix) > apiKeyPrefixLen {
		prefix = prefix[:apiKeyPrefixLen]
	}

	record, err := s.getByPrefix(ctx, prefix)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errorx.ErrUnauthorized
		}
		return nil, err
	}

	if record.Status != "active" {
		return nil, errorx.ErrUnauthorized
	}
	if record.ExpiresAt.Valid && time.Now().After(record.ExpiresAt.Time) {
		return nil, errorx.ErrUnauthorized
	}
	if record.KeyHash != utils.HashSecret(apiKey, s.cfg.APIKeyPepper) {
		return nil, errorx.ErrUnauthorized
	}

	for _, scope := range requiredScopes {
		if !hasScope(record.Scopes, scope) {
			return nil, errorx.ErrForbidden
		}
	}

	_ = s.q.UpdateAPIKeyLastUsedAt(ctx, record.ID)

	return &APIKeyIdentity{
		APIKeyID: record.ID,
		UserID:   record.UserID,
		Scopes:   record.Scopes,
	}, nil
}

func (s *APIKeyService) getByPrefix(ctx context.Context, prefix string) (dbgen.ApiKey, error) {
	if raw, err := s.rdb.Get(ctx, cache.APIKeyKey(prefix)).Result(); err == nil {
		var cached apiKeyCacheValue
		if json.Unmarshal([]byte(raw), &cached) == nil {
			apiKeyID, err1 := uuid.Parse(cached.ID)
			userID, err2 := uuid.Parse(cached.UserID)
			if err1 == nil && err2 == nil {
				record := dbgen.ApiKey{
					ID:        apiKeyID,
					UserID:    userID,
					KeyPrefix: prefix,
					KeyHash:   cached.KeyHash,
					Scopes:    cached.Scopes,
					Status:    cached.Status,
				}
				if cached.ExpiresAt != nil {
					record.ExpiresAt = pgtype.Timestamptz{Time: *cached.ExpiresAt, Valid: true}
				}
				return record, nil
			}
		}
	}

	record, err := s.q.GetAPIKeyByPrefix(ctx, prefix)
	if err != nil {
		return dbgen.ApiKey{}, err
	}
	_ = s.cacheAPIKey(ctx, record)
	return record, nil
}

func (s *APIKeyService) cacheAPIKey(ctx context.Context, record dbgen.ApiKey) error {
	payload := apiKeyCacheValue{
		ID:      record.ID.String(),
		UserID:  record.UserID.String(),
		KeyHash: record.KeyHash,
		Scopes:  record.Scopes,
		Status:  record.Status,
	}
	if record.ExpiresAt.Valid {
		expires := record.ExpiresAt.Time
		payload.ExpiresAt = &expires
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, cache.APIKeyKey(record.KeyPrefix), raw, s.cfg.APIKeyCacheTTL).Err()
}

func normalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{"transactions:read", "transactions:write", "stats:read", "categories:read"}
	}

	seen := make(map[string]struct{}, len(scopes))
	normalized := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	return normalized
}

func hasScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}
