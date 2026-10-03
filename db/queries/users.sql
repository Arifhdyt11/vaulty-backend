-- name: CreateUser :one
INSERT INTO users (email, name, password_hash, google_sub, avatar_url)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByGoogleSub :one
SELECT * FROM users WHERE google_sub = $1;

-- name: LinkGoogleAccount :one
UPDATE users
SET google_sub = $2,
    avatar_url = COALESCE(avatar_url, $3),
    name = CASE WHEN name = '' THEN $4 ELSE name END,
    updated_at = now()
WHERE id = $1
RETURNING *;
