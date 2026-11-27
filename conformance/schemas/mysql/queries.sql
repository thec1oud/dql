-- The running example's query file on MySQL: the queries of design record
-- §17.3, a list and a write. MySQL numbers its ? placeholders by position,
-- so the same parameter can appear twice.

-- name: GetUserByEmail :one
-- returns: user.full
SELECT id, tenant_id, email, display_name, created_at, updated_at, version
FROM users
WHERE tenant_id = @scope.tenant_id AND email = @email;

-- name: AuthorPostCounts :many
-- consistency: any
SELECT u.id AS author_id, u.display_name, count(p.id) AS posts
FROM users u
LEFT JOIN posts p ON p.author_id = u.id
WHERE u.tenant_id = @scope.tenant_id
GROUP BY u.id, u.display_name;

-- name: UsersByEmail :many
SELECT id, email
FROM users
WHERE tenant_id = @scope.tenant_id AND email IN (sqlc.slice(emails))
ORDER BY id;

-- name: SearchUsers :many
SELECT id, display_name
FROM users
WHERE tenant_id = @scope.tenant_id AND (@name = '' OR display_name LIKE concat('%', @name, '%'))
ORDER BY id
LIMIT @lim;

-- name: RenameUser :execrows
UPDATE users
SET display_name = sqlc.narg(display_name), version = version + 1
WHERE tenant_id = @scope.tenant_id AND id = @id AND version = @version;
