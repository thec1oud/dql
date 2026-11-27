-- The running example's query file: the queries of design record §17.3, and
-- a write.

-- name: GetUserByEmail :one
-- returns: user.full
SELECT id, tenant_id, email, display_name, created_at, updated_at, version
FROM app.users
WHERE tenant_id = @scope.tenant_id AND email = @email;

-- name: AuthorPostCounts :many
-- consistency: any
SELECT u.id AS author_id, u.display_name, count(p.id) AS posts
FROM app.users u
LEFT JOIN app.posts p ON p.author_id = u.id
WHERE u.tenant_id = @scope.tenant_id
GROUP BY u.id, u.display_name;

-- name: RenameUser :execrows
UPDATE app.users
SET display_name = sqlc.narg(display_name), version = version + 1
WHERE tenant_id = @scope.tenant_id AND id = @id AND version = @version;
