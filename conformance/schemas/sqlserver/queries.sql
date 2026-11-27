-- The running example's query file on SQL Server: the queries of design
-- record §17.3, a list and a write. Each parameter becomes @p1, @p2 and on,
-- numbered by the query's parameters, so the same parameter can appear
-- twice. SQL Server finds an unqualified name in the login's default
-- schema, so each table is named with its schema.

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

-- name: UsersByEmail :many
SELECT id, email
FROM app.users
WHERE tenant_id = @scope.tenant_id AND email IN (sqlc.slice(emails))
ORDER BY id;

-- name: SearchUsers :many
SELECT TOP (@lim) id, display_name
FROM app.users
WHERE tenant_id = @scope.tenant_id AND (@name = N'' OR display_name LIKE N'%' + @name + N'%')
ORDER BY id;

-- name: RenameUser :execrows
UPDATE app.users
SET display_name = sqlc.narg(display_name), version = version + 1
WHERE tenant_id = @scope.tenant_id AND id = @id AND version = @version;
