-- The running example `shop` (design record §1.3) on SQL Server. The
-- database is the catalog and app its namespace, as on Postgres. What SQL
-- Server cannot hold is left out or narrowed: no domains (email and slug
-- checks sit on their columns), no enum type (status is a membership check
-- named an enum), no composite (price is left out), no arrays (labels is
-- left out) and no overloads (post_score takes one argument). SQL Server has
-- no instant type, so times are datetimeoffset, instants with their offset,
-- which is always +00:00 here (§8.8). An IDENTITY column is always
-- generated, so posts.id is too.
-- Text is nvarchar, and title is bounded so that slug can be indexed.
-- Length checks use DATALENGTH, two bytes per UTF-16 code unit: LEN
-- ignores trailing spaces, so a length read from it would not be exact.
--
-- sqlserver-live reads annotations from MS_Description extended properties,
-- the comments SQL Server keeps on tables, columns, constraints, views and
-- functions. Batches are separated by GO lines, as sqlcmd reads them.

CREATE SCHEMA app;
GO

CREATE TABLE app.tenants (
  id   UNIQUEIDENTIFIER NOT NULL CONSTRAINT tenants_id_default DEFAULT NEWID(),
  name NVARCHAR(MAX) NOT NULL,
  CONSTRAINT tenants_pkey PRIMARY KEY (id)
);

CREATE TABLE app.users (
  id            BIGINT IDENTITY(1, 1) NOT NULL,
  tenant_id     UNIQUEIDENTIFIER NOT NULL,
  email         NVARCHAR(320) NOT NULL,
  display_name  NVARCHAR(80),
  password_hash NVARCHAR(MAX) NOT NULL,
  created_at    DATETIMEOFFSET(6) NOT NULL CONSTRAINT users_created_at_default DEFAULT SYSUTCDATETIME(),
  updated_at    DATETIMEOFFSET(6) NOT NULL CONSTRAINT users_updated_at_default DEFAULT SYSUTCDATETIME(),
  version       INT NOT NULL CONSTRAINT users_version_default DEFAULT 1,
  CONSTRAINT users_pkey PRIMARY KEY (id),
  CONSTRAINT users_email_check CHECK (DATALENGTH(email) <= 640),
  CONSTRAINT users_tenant_email_key UNIQUE (tenant_id, email),
  CONSTRAINT users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES app.tenants (id)
);

CREATE TABLE app.profiles (
  user_id  BIGINT NOT NULL,
  bio      NVARCHAR(MAX),
  birthday DATE,
  CONSTRAINT profiles_pkey PRIMARY KEY (user_id),
  CONSTRAINT profiles_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id)
);

CREATE TABLE app.posts (
  id           BIGINT IDENTITY(1, 1) NOT NULL,
  author_id    BIGINT NOT NULL,
  editor_id    BIGINT,
  title        NVARCHAR(200) NOT NULL,
  slug         AS (CAST(LOWER(REPLACE(title, ' ', '-')) AS NVARCHAR(200))) PERSISTED,
  status       NVARCHAR(16) NOT NULL CONSTRAINT posts_status_default DEFAULT 'draft',
  published_at DATETIMEOFFSET(6),
  CONSTRAINT posts_pkey PRIMARY KEY (id),
  CONSTRAINT posts_status_check CHECK (status IN ('draft', 'published', 'archived')),
  CONSTRAINT posts_title_check CHECK (DATALENGTH(title) BETWEEN 2 AND 400),
  CONSTRAINT posts_published_check CHECK (published_at IS NULL OR status <> 'draft'),
  CONSTRAINT posts_author_id_fkey FOREIGN KEY (author_id) REFERENCES app.users (id),
  CONSTRAINT posts_editor_id_fkey FOREIGN KEY (editor_id) REFERENCES app.users (id)
);

CREATE TABLE app.tags (
  id   BIGINT IDENTITY(1, 1) NOT NULL,
  name NVARCHAR(200) NOT NULL,
  CONSTRAINT tags_pkey PRIMARY KEY (id),
  CONSTRAINT tags_name_key UNIQUE (name)
);

CREATE TABLE app.post_tags (
  post_id BIGINT NOT NULL,
  tag_id  BIGINT NOT NULL,
  CONSTRAINT post_tags_pkey PRIMARY KEY (post_id, tag_id),
  CONSTRAINT post_tags_post_id_fkey FOREIGN KEY (post_id) REFERENCES app.posts (id),
  CONSTRAINT post_tags_tag_id_fkey FOREIGN KEY (tag_id) REFERENCES app.tags (id)
);

CREATE UNIQUE INDEX posts_one_published_slug ON app.posts (slug) WHERE status = 'published';
GO

-- updated_at changes with every update that leaves it as it was, as
-- Postgres's shop has its application do and MySQL's ON UPDATE does.
CREATE TRIGGER app.users_updated_at ON app.users AFTER UPDATE AS
BEGIN
  SET NOCOUNT ON;
  IF NOT UPDATE(updated_at)
    UPDATE u SET updated_at = SYSUTCDATETIME()
    FROM app.users u JOIN inserted i ON u.id = i.id;
END;
GO

CREATE VIEW app.published_posts AS
  SELECT id, title, author_id, published_at
  FROM app.posts WHERE status = 'published';
GO

CREATE FUNCTION app.post_score(@p_id BIGINT) RETURNS DECIMAL(38, 10) AS
BEGIN
  RETURN 0;
END;
GO

-- Annotations (§7).
EXEC sp_addextendedproperty 'MS_Description', '@bound', 'SCHEMA', 'app', 'TABLE', 'users', 'COLUMN', 'tenant_id';
EXEC sp_addextendedproperty 'MS_Description', '@write-only @sensitive', 'SCHEMA', 'app', 'TABLE', 'users', 'COLUMN', 'password_hash';
EXEC sp_addextendedproperty 'MS_Description', '@immutable', 'SCHEMA', 'app', 'TABLE', 'users', 'COLUMN', 'created_at';
EXEC sp_addextendedproperty 'MS_Description', '@db-updated', 'SCHEMA', 'app', 'TABLE', 'users', 'COLUMN', 'updated_at';
EXEC sp_addextendedproperty 'MS_Description', '@version', 'SCHEMA', 'app', 'TABLE', 'users', 'COLUMN', 'version';
EXEC sp_addextendedproperty 'MS_Description', '@enum(post_status)', 'SCHEMA', 'app', 'TABLE', 'posts', 'CONSTRAINT', 'posts_status_check';
EXEC sp_addextendedproperty 'MS_Description', '@inverse-name(user_profile)', 'SCHEMA', 'app', 'TABLE', 'profiles', 'CONSTRAINT', 'profiles_user_id_fkey';
EXEC sp_addextendedproperty 'MS_Description', '@logical-key(id)', 'SCHEMA', 'app', 'VIEW', 'published_posts';
EXEC sp_addextendedproperty 'MS_Description', '@nonnull', 'SCHEMA', 'app', 'VIEW', 'published_posts', 'COLUMN', 'id';
EXEC sp_addextendedproperty 'MS_Description', '@nonnull', 'SCHEMA', 'app', 'VIEW', 'published_posts', 'COLUMN', 'title';
EXEC sp_addextendedproperty 'MS_Description', '@public', 'SCHEMA', 'app', 'FUNCTION', 'post_score';
