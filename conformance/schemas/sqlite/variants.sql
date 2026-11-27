-- The design record's variants of the running example on SQLite, for the
-- scenarios shop's own schema cannot hold: a soft-delete column on posts
-- (§21.3), an instant stored as text as published_at is. Its as(instant) is
-- in variants.yaml: SQLite appends the column to the table's text after
-- published_at's comment.
ALTER TABLE posts ADD deleted_at TEXT;
