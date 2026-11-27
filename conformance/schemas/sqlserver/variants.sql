-- The design record's variants of the running example on SQL Server, for the
-- scenarios shop's own schema cannot hold: a soft-delete column on posts
-- (§21.3).
ALTER TABLE app.posts ADD deleted_at DATETIMEOFFSET(6);
