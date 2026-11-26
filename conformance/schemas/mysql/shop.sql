-- The running example `shop` (design record §1.3) on MySQL and MariaDB. The
-- database is the catalog, with one implicit namespace (§5.1), so tables
-- have no app. prefix. What MySQL cannot hold is left out or narrowed:
-- no domains (email and slug checks sit on their columns), no composite
-- (price is left out), no arrays (labels is left out), no partial index,
-- and uuid is CHAR(36) narrowed by as(uuid). The function bodies return 0.
-- posts.featured is not in the record: a TINYINT(1) annotated bool (§8.7).

CREATE TABLE tenants (
  id   CHAR(36) NOT NULL DEFAULT (uuid()) COMMENT '@as(uuid)',
  name TEXT NOT NULL,
  PRIMARY KEY (id)
);

CREATE TABLE users (
  id            BIGINT NOT NULL AUTO_INCREMENT,
  tenant_id     CHAR(36) NOT NULL COMMENT '@bound @as(uuid)',
  email         VARCHAR(320) NOT NULL,
  display_name  VARCHAR(80),
  password_hash TEXT NOT NULL COMMENT '@write-only @sensitive',
  created_at    TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '@immutable',
  updated_at    TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  version       INT NOT NULL DEFAULT 1 COMMENT '@version',
  PRIMARY KEY (id),
  CONSTRAINT users_tenant_email_key UNIQUE (tenant_id, email),
  CONSTRAINT users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id)
);

CREATE TABLE profiles (
  user_id  BIGINT NOT NULL,
  bio      TEXT,
  birthday DATE,
  PRIMARY KEY (user_id),
  CONSTRAINT profiles_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id)
);

CREATE TABLE posts (
  id           BIGINT NOT NULL AUTO_INCREMENT,
  author_id    BIGINT NOT NULL,
  editor_id    BIGINT,
  title        TEXT NOT NULL,
  slug         TEXT GENERATED ALWAYS AS (lower(replace(title, ' ', '-'))) STORED,
  status       ENUM('draft', 'published', 'archived') NOT NULL DEFAULT 'draft',
  published_at TIMESTAMP(6) NULL,
  featured     TINYINT(1) NOT NULL DEFAULT 0 COMMENT '@bool',
  PRIMARY KEY (id),
  CONSTRAINT posts_title_check CHECK (char_length(title) BETWEEN 1 AND 200),
  CONSTRAINT posts_published_check CHECK (published_at IS NULL OR status <> 'draft'),
  CONSTRAINT posts_author_id_fkey FOREIGN KEY (author_id) REFERENCES users (id),
  CONSTRAINT posts_editor_id_fkey FOREIGN KEY (editor_id) REFERENCES users (id)
);

CREATE TABLE tags (
  id   BIGINT NOT NULL AUTO_INCREMENT,
  name VARCHAR(200) NOT NULL,
  PRIMARY KEY (id),
  CONSTRAINT tags_name_key UNIQUE (name),
  CONSTRAINT tags_name_check CHECK (regexp_like(name, '^[a-z0-9]+(-[a-z0-9]+)*$', 'c'))
);

CREATE TABLE post_tags (
  post_id BIGINT NOT NULL,
  tag_id  BIGINT NOT NULL,
  PRIMARY KEY (post_id, tag_id),
  CONSTRAINT post_tags_post_id_fkey FOREIGN KEY (post_id) REFERENCES posts (id),
  CONSTRAINT post_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES tags (id)
);

CREATE VIEW published_posts AS
  SELECT id, title, author_id, published_at
  FROM posts WHERE status = 'published';

CREATE FUNCTION post_score(p_id BIGINT) RETURNS DECIMAL(65, 30)
  DETERMINISTIC READS SQL DATA COMMENT '@public' RETURN 0;
