-- The running example `shop` (design record §1.3) on SQLite. The database
-- file is the catalog, and its main schema the implicit namespace (§5.1),
-- so tables have no app. prefix. Tables are STRICT, so their columns hold
-- one storage class each and are not weak (§8.10). What SQLite cannot hold
-- is left out or narrowed: no domains (email and slug checks sit on their
-- columns), no enum type (status is a membership check named an enum), no
-- composite (price is left out), no arrays (labels is left out), no
-- functions, and times and uuids are TEXT narrowed by as() (§7.2).
--
-- sqlite-live reads annotations from comments: one inside a column's or a
-- constraint's definition, or after its comma on the same line, is that
-- column's or constraint's; one between the table's opening parenthesis and
-- its first definition is the table's.

CREATE TABLE tenants (
  id   TEXT NOT NULL PRIMARY KEY, -- @as(uuid)
  name TEXT NOT NULL
) STRICT;

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  tenant_id     TEXT NOT NULL, -- @bound @as(uuid)
  email         TEXT NOT NULL,
  display_name  TEXT,
  password_hash TEXT NOT NULL, -- @write-only @sensitive
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), -- @immutable @as(instant)
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), -- @db-updated @as(instant)
  version       INTEGER NOT NULL DEFAULT 1, -- @version
  CONSTRAINT users_email_check CHECK (length(email) <= 320),
  CONSTRAINT users_display_name_check CHECK (length(display_name) <= 80),
  CONSTRAINT users_tenant_email_key UNIQUE (tenant_id, email),
  CONSTRAINT users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) STRICT;

CREATE TRIGGER users_updated_at AFTER UPDATE ON users FOR EACH ROW
  WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE users SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = NEW.id;
END;

-- user_id is INT, not INTEGER, so it is no rowid alias that SQLite would
-- number when an insert leaves it out: a profile names its user.
CREATE TABLE profiles (
  user_id  INT NOT NULL PRIMARY KEY,
  bio      TEXT,
  birthday TEXT, -- @as(date)
  CONSTRAINT profiles_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id)
) STRICT;

CREATE TABLE posts (
  id           INTEGER PRIMARY KEY,
  author_id    INTEGER NOT NULL,
  editor_id    INTEGER,
  title        TEXT NOT NULL,
  slug         TEXT GENERATED ALWAYS AS (lower(replace(title, ' ', '-'))) STORED,
  status       TEXT NOT NULL DEFAULT 'draft',
  published_at TEXT, -- @as(instant)
  CONSTRAINT posts_status_check CHECK (status IN ('draft', 'published', 'archived')), -- @enum(post_status)
  CONSTRAINT posts_title_check CHECK (length(title) BETWEEN 1 AND 200),
  CONSTRAINT posts_published_check CHECK (published_at IS NULL OR status <> 'draft'),
  CONSTRAINT posts_author_id_fkey FOREIGN KEY (author_id) REFERENCES users (id),
  CONSTRAINT posts_editor_id_fkey FOREIGN KEY (editor_id) REFERENCES users (id)
) STRICT;

CREATE TABLE tags (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  CONSTRAINT tags_name_key UNIQUE (name),
  CONSTRAINT tags_name_check CHECK (length(name) <= 200)
) STRICT;

CREATE TABLE post_tags (
  post_id INTEGER NOT NULL,
  tag_id  INTEGER NOT NULL,
  PRIMARY KEY (post_id, tag_id),
  CONSTRAINT post_tags_post_id_fkey FOREIGN KEY (post_id) REFERENCES posts (id),
  CONSTRAINT post_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags (id)
) STRICT;

CREATE VIEW published_posts AS
  SELECT id, title, author_id, published_at
  FROM posts WHERE status = 'published';
