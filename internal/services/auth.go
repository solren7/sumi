package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sumi/config"
	"sumi/internal/cache"
	"sumi/internal/repository/dbgen"
	"sumi/pkg/errorx"
	"sumi/pkg/utils"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type SessionMeta struct {
	DeviceID  *string
	UserAgent *string
	IPAddress *string
}

type refreshTokenCacheValue struct {
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
}

type AuthService struct {
	pool *pgxpool.Pool
	q   *dbgen.Queries
	cfg *config.Config
	rdb redis.UniversalClient
}

func NewAuthService(pool *pgxpool.Pool, q *dbgen.Queries, cfg *config.Config, rdb redis.UniversalClient) *AuthService {
	return &AuthService{pool: pool, q: q, cfg: cfg, rdb: rdb}
}

type RegisterInput struct {
	Email    string
	Password string
	Username string
}

type AuthOutput struct {
	AccessToken  string
	RefreshToken string
	User         *dbgen.User
}

type categoryTemplate struct {
	Expense []categoryTemplateGroup `json:"expense"`
	Income  []categoryTemplateGroup `json:"income"`
}

type categoryTemplateGroup struct {
	Name      string                  `json:"name"`
	SortOrder int32                   `json:"sort_order"`
	Children  []categoryTemplateChild `json:"children"`
}

type categoryTemplateChild struct {
	Name      string `json:"name"`
	SortOrder int32  `json:"sort_order"`
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *AuthService) CheckEmailExists(ctx context.Context, email string) (bool, error) {
	_, err := s.q.GetUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *AuthService) Register(ctx context.Context, input RegisterInput, meta SessionMeta) (*AuthOutput, error) {
	email := normalizeEmail(input.Email)
	if email == "" || strings.TrimSpace(input.Password) == "" {
		return nil, errorx.New(400, "Email and password are required")
	}
	if len(input.Password) < 8 {
		return nil, errorx.New(400, "Password must be at least 8 characters")
	}
	if len(input.Password) > 128 {
		return nil, errorx.New(400, "Password must be at most 128 characters")
	}
	if len(input.Username) > 64 {
		return nil, errorx.New(400, "Username must be at most 64 characters")
	}

	exists, err := s.CheckEmailExists(ctx, email)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorx.New(409, "Email already registered")
	}

	username := strings.TrimSpace(input.Username)
	if username == "" {
		parts := strings.Split(email, "@")
		username = parts[0]
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	txQueries := s.q.WithTx(tx)
	user, err := txQueries.CreateUser(ctx, dbgen.CreateUserParams{
		Email:           email,
		Username:        username,
		PasswordHash:    string(hashedPassword),
		DefaultCurrency: s.cfg.DefaultCurrency,
		Timezone:        s.cfg.DefaultTimezone,
	})
	if err != nil {
		return nil, err
	}

	if err := s.initializeUserCategories(ctx, txQueries, user.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return s.issueAuthOutput(ctx, user, meta)
}

func (s *AuthService) Login(ctx context.Context, email, password string, meta SessionMeta) (*AuthOutput, error) {
	user, err := s.q.GetUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errorx.New(401, "Invalid credentials")
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, errorx.New(401, "Invalid credentials")
	}

	return s.issueAuthOutput(ctx, user, meta)
}

func (s *AuthService) Refresh(ctx context.Context, refreshToken string, meta SessionMeta) (*AuthOutput, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, errorx.New(400, "Refresh token is required")
	}

	tokenHash := utils.HashSecret(refreshToken, s.cfg.RefreshTokenPepper)

	refreshRecord, err := s.getRefreshTokenRecord(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if refreshRecord.RevokedAt.Valid {
		return nil, errorx.New(401, "Refresh token has been revoked")
	}
	if time.Now().After(refreshRecord.ExpiresAt) {
		return nil, errorx.New(401, "Refresh token has expired")
	}

	if err := s.q.RevokeRefreshTokenByHash(ctx, tokenHash); err != nil {
		return nil, err
	}
	_ = s.rdb.Del(ctx, cache.RefreshTokenKey(tokenHash)).Err()

	user, err := s.q.GetUserById(ctx, refreshRecord.UserID)
	if err != nil {
		return nil, err
	}

	return s.issueAuthOutput(ctx, user, meta)
}

func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return errorx.New(400, "Refresh token is required")
	}

	tokenHash := utils.HashSecret(refreshToken, s.cfg.RefreshTokenPepper)
	if err := s.q.RevokeRefreshTokenByHash(ctx, tokenHash); err != nil {
		return err
	}
	_ = s.rdb.Del(ctx, cache.RefreshTokenKey(tokenHash)).Err()
	return nil
}

func (s *AuthService) GetMe(ctx context.Context, userID uuid.UUID) (*dbgen.User, error) {
	user, err := s.q.GetUserById(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errorx.ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *AuthService) ParseAccessToken(tokenString string) (*utils.Claims, error) {
	claims, err := utils.ParseAccessToken(tokenString, s.cfg.JWTSecret)
	if err != nil {
		return nil, errorx.New(401, "Invalid or expired token")
	}
	return claims, nil
}

func (s *AuthService) issueAuthOutput(ctx context.Context, user dbgen.User, meta SessionMeta) (*AuthOutput, error) {
	accessToken, err := utils.GenerateAccessToken(user.ID.String(), user.Email, s.cfg.JWTSecret, s.cfg.AccessTokenTTL)
	if err != nil {
		return nil, err
	}

	refreshToken, err := utils.GenerateOpaqueToken(32)
	if err != nil {
		return nil, err
	}

	tokenHash := utils.HashSecret(refreshToken, s.cfg.RefreshTokenPepper)
	expiresAt := time.Now().Add(s.cfg.RefreshTokenTTL)

	if _, err := s.q.CreateRefreshToken(ctx, dbgen.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: tokenHash,
		DeviceID:  meta.DeviceID,
		UserAgent: meta.UserAgent,
		IpAddress: meta.IPAddress,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, err
	}

	if err := s.cacheRefreshToken(ctx, tokenHash, refreshTokenCacheValue{
		UserID:    user.ID.String(),
		ExpiresAt: expiresAt,
		Revoked:   false,
	}); err != nil {
		return nil, err
	}

	return &AuthOutput{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         &user,
	}, nil
}

func (s *AuthService) cacheRefreshToken(ctx context.Context, tokenHash string, value refreshTokenCacheValue) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}

	ttl := time.Until(value.ExpiresAt) + s.cfg.RefreshTokenCacheExtra
	if ttl <= 0 {
		ttl = time.Minute
	}

	return s.rdb.Set(ctx, cache.RefreshTokenKey(tokenHash), payload, ttl).Err()
}

func (s *AuthService) getRefreshTokenRecord(ctx context.Context, tokenHash string) (dbgen.RefreshToken, error) {
	var cached refreshTokenCacheValue
	if raw, err := s.rdb.Get(ctx, cache.RefreshTokenKey(tokenHash)).Result(); err == nil {
		if err := json.Unmarshal([]byte(raw), &cached); err == nil {
			userID, parseErr := uuid.Parse(cached.UserID)
			if parseErr == nil {
				return dbgen.RefreshToken{
					UserID:    userID,
					TokenHash: tokenHash,
					ExpiresAt: cached.ExpiresAt,
				}, nil
			}
		}
	}

	record, err := s.q.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return dbgen.RefreshToken{}, errorx.New(401, "Invalid refresh token")
		}
		return dbgen.RefreshToken{}, err
	}

	_ = s.cacheRefreshToken(ctx, tokenHash, refreshTokenCacheValue{
		UserID:    record.UserID.String(),
		ExpiresAt: record.ExpiresAt,
		Revoked:   record.RevokedAt.Valid,
	})

	return record, nil
}

func (s *AuthService) initializeUserCategories(ctx context.Context, q *dbgen.Queries, userID uuid.UUID) error {
	cfg, err := q.GetSystemConfigByTypeAndKey(ctx, dbgen.GetSystemConfigByTypeAndKeyParams{
		Type: "category_template",
		Key:  "default_categories",
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return errorx.New(500, "Default category template is missing")
		}
		return err
	}

	var tpl categoryTemplate
	if err := json.Unmarshal(cfg.Value, &tpl); err != nil {
		return fmt.Errorf("parse category template config: %w", err)
	}

	userUUID := pgtype.UUID{Bytes: userID, Valid: true}
	if err := insertCategoryTemplateGroups(ctx, q, userUUID, 1, tpl.Expense); err != nil {
		return err
	}
	if err := insertCategoryTemplateGroups(ctx, q, userUUID, 2, tpl.Income); err != nil {
		return err
	}

	return nil
}

func insertCategoryTemplateGroups(ctx context.Context, q *dbgen.Queries, userID pgtype.UUID, categoryType int16, groups []categoryTemplateGroup) error {
	for _, group := range groups {
		parent, err := q.CreateCategory(ctx, dbgen.CreateCategoryParams{
			UserID:    userID,
			Type:      categoryType,
			Name:      strings.TrimSpace(group.Name),
			Level:     1,
			SortOrder: group.SortOrder,
			IsSystem:  false,
			IsActive:  true,
		})
		if err != nil {
			return err
		}

		for _, child := range group.Children {
			if _, err := q.CreateCategory(ctx, dbgen.CreateCategoryParams{
				UserID:    userID,
				Type:      categoryType,
				Name:      strings.TrimSpace(child.Name),
				ParentID:  &parent.ID,
				Level:     2,
				SortOrder: child.SortOrder,
				IsSystem:  false,
				IsActive:  true,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}
