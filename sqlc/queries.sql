-- name: CreateUser :one
INSERT INTO users (
    email, username, password_hash, default_currency, timezone
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1
LIMIT 1;

-- name: GetUserById :one
SELECT * FROM users
WHERE id = $1
LIMIT 1;

-- name: GetSystemConfigByTypeAndKey :one
SELECT * FROM configs
WHERE type = $1
  AND key = $2
  AND user_id IS NULL
  AND status = 'active'
LIMIT 1;

-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (
    user_id, token_hash, device_id, user_agent, ip_address, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1
LIMIT 1;

-- name: RevokeRefreshTokenByHash :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE token_hash = $1;

-- name: DeleteExpiredRefreshTokens :exec
DELETE FROM refresh_tokens
WHERE expires_at < NOW() OR revoked_at IS NOT NULL;

-- name: CreateAPIKey :one
INSERT INTO api_keys (
    user_id, name, key_prefix, key_hash, scopes, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: ListAPIKeysByUser :many
SELECT * FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: GetAPIKeyByPrefix :one
SELECT * FROM api_keys
WHERE key_prefix = $1
LIMIT 1;

-- name: GetAPIKeyByID :one
SELECT * FROM api_keys
WHERE id = $1 AND user_id = $2
LIMIT 1;

-- name: UpdateAPIKeyLastUsedAt :exec
UPDATE api_keys
SET last_used_at = NOW()
WHERE id = $1;

-- name: RevokeAPIKey :exec
UPDATE api_keys
SET status = 'revoked'
WHERE id = $1 AND user_id = $2;

-- name: CreateCategory :one
INSERT INTO categories (
    user_id, type, name, parent_id, level, sort_order, icon, is_system, is_active
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
) RETURNING *;

-- name: ListCategoriesByUserAndType :many
SELECT * FROM categories
WHERE user_id = $1
  AND is_active = TRUE
  AND type = $2
ORDER BY level ASC, parent_id ASC NULLS FIRST, sort_order ASC, id ASC;

-- name: GetCategoryByIDAndUser :one
SELECT * FROM categories
WHERE id = $1 AND user_id = $2
LIMIT 1;

-- name: CreateBill :one
INSERT INTO bills (
    user_id, type, amount, currency, category_id, description, occurred_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: GetBillByID :one
SELECT * FROM bills
WHERE id = $1 AND user_id = $2
LIMIT 1;

-- name: UpdateBill :one
UPDATE bills
SET
    type = $2,
    amount = $3,
    currency = $4,
    category_id = $5,
    description = $6,
    occurred_at = $7
WHERE id = $1 AND user_id = $8
RETURNING *;

-- name: DeleteBill :exec
DELETE FROM bills
WHERE id = $1 AND user_id = $2;

-- name: ListBills :many
SELECT * FROM bills
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(type)::smallint IS NULL OR type = sqlc.narg(type)::smallint)
  AND (sqlc.narg(category_id)::bigint IS NULL OR category_id = sqlc.narg(category_id)::bigint)
  AND (sqlc.narg(currency)::char(3) IS NULL OR currency = sqlc.narg(currency)::char(3))
  AND (sqlc.narg(start_time)::timestamptz IS NULL OR occurred_at >= sqlc.narg(start_time)::timestamptz)
  AND (sqlc.narg(end_time)::timestamptz IS NULL OR occurred_at < sqlc.narg(end_time)::timestamptz)
  AND (sqlc.narg(keyword)::text IS NULL OR description ILIKE sqlc.narg(keyword)::text)
ORDER BY occurred_at DESC
LIMIT sqlc.arg(limit_count) OFFSET sqlc.arg(offset_count);

-- name: GetMonthlyStats :many
SELECT
    currency,
    COALESCE(SUM(CASE WHEN type = 2 THEN amount ELSE 0 END), 0)::DECIMAL AS total_income,
    COALESCE(SUM(CASE WHEN type = 1 THEN amount ELSE 0 END), 0)::DECIMAL AS total_expense
FROM bills
WHERE user_id = sqlc.arg(user_id)
  AND occurred_at >= sqlc.arg(start_time)
  AND occurred_at < sqlc.arg(end_time)
GROUP BY currency
ORDER BY currency ASC;

-- name: GetDailyStats :many
SELECT
    timezone(sqlc.arg(user_timezone)::text, occurred_at)::date AS date,
    currency,
    COALESCE(SUM(CASE WHEN type = 2 THEN amount ELSE 0 END), 0)::DECIMAL AS income,
    COALESCE(SUM(CASE WHEN type = 1 THEN amount ELSE 0 END), 0)::DECIMAL AS expense
FROM bills
WHERE user_id = sqlc.arg(user_id)
  AND occurred_at >= sqlc.arg(start_time)
  AND occurred_at < sqlc.arg(end_time)
GROUP BY timezone(sqlc.arg(user_timezone), occurred_at)::date, currency
ORDER BY date ASC, currency ASC;

-- name: GetCategoryStats :many
SELECT
    parent.id AS parent_category_id,
    parent.name AS parent_category_name,
    child.id AS category_id,
    child.name AS category_name,
    bills.currency,
    COALESCE(SUM(bills.amount), 0)::DECIMAL AS amount
FROM bills
JOIN categories child ON child.id = bills.category_id
LEFT JOIN categories parent ON parent.id = child.parent_id
WHERE bills.user_id = sqlc.arg(user_id)
  AND bills.type = sqlc.arg(type)
  AND bills.occurred_at >= sqlc.arg(start_time)
  AND bills.occurred_at < sqlc.arg(end_time)
GROUP BY parent.id, parent.name, child.id, child.name, bills.currency
ORDER BY amount DESC, category_id ASC;
