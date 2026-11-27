-- The design record's variants of the running example on MySQL, for the
-- scenarios shop's own schema cannot hold: a soft-delete column on posts
-- (§21.3).
ALTER TABLE posts ADD deleted_at TIMESTAMP(6) NULL;
