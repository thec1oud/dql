CREATE SCHEMA [app];
GO
CREATE TABLE [app].[post_tags] (
  [post_id] bigint NOT NULL,
  [tag_id] bigint NOT NULL,
  CONSTRAINT [post_tags_pkey] PRIMARY KEY CLUSTERED ([post_id], [tag_id])
);
GO
CREATE TABLE [app].[posts] (
  [id] bigint IDENTITY(1, 1) NOT NULL,
  [author_id] bigint NOT NULL,
  [editor_id] bigint NULL,
  [title] nvarchar(200) COLLATE SQL_Latin1_General_CP1_CI_AS NOT NULL,
  [slug] AS (CONVERT([nvarchar](200),lower(replace([title],' ','-')))) PERSISTED,
  [status] nvarchar(16) COLLATE SQL_Latin1_General_CP1_CI_AS NOT NULL CONSTRAINT [posts_status_default] DEFAULT ('draft'),
  [published_at] datetimeoffset(6) NULL,
  CONSTRAINT [posts_pkey] PRIMARY KEY CLUSTERED ([id]),
  CONSTRAINT [posts_published_check] CHECK ([published_at] IS NULL OR [status]<>'draft'),
  CONSTRAINT [posts_status_check] CHECK ([status] IN ('draft', 'published', 'archived')),
  CONSTRAINT [posts_title_check] CHECK (DATALENGTH([title]) BETWEEN 2 AND 400)
);
GO
CREATE TABLE [app].[profiles] (
  [user_id] bigint NOT NULL,
  [bio] nvarchar(max) COLLATE SQL_Latin1_General_CP1_CI_AS NULL,
  [birthday] date NULL,
  CONSTRAINT [profiles_pkey] PRIMARY KEY CLUSTERED ([user_id])
);
GO
CREATE TABLE [app].[tags] (
  [id] bigint IDENTITY(1, 1) NOT NULL,
  [name] nvarchar(200) COLLATE SQL_Latin1_General_CP1_CI_AS NOT NULL,
  CONSTRAINT [tags_name_key] UNIQUE NONCLUSTERED ([name]),
  CONSTRAINT [tags_pkey] PRIMARY KEY CLUSTERED ([id])
);
GO
CREATE TABLE [app].[tenants] (
  [id] uniqueidentifier NOT NULL CONSTRAINT [tenants_id_default] DEFAULT (newid()),
  [name] nvarchar(max) COLLATE SQL_Latin1_General_CP1_CI_AS NOT NULL,
  CONSTRAINT [tenants_pkey] PRIMARY KEY CLUSTERED ([id])
);
GO
CREATE TABLE [app].[users] (
  [id] bigint IDENTITY(1, 1) NOT NULL,
  [tenant_id] uniqueidentifier NOT NULL,
  [email] nvarchar(320) COLLATE SQL_Latin1_General_CP1_CI_AS NOT NULL,
  [display_name] nvarchar(80) COLLATE SQL_Latin1_General_CP1_CI_AS NULL,
  [password_hash] nvarchar(max) COLLATE SQL_Latin1_General_CP1_CI_AS NOT NULL,
  [created_at] datetimeoffset(6) NOT NULL CONSTRAINT [users_created_at_default] DEFAULT (sysutcdatetime()),
  [updated_at] datetimeoffset(6) NOT NULL CONSTRAINT [users_updated_at_default] DEFAULT (sysutcdatetime()),
  [version] int NOT NULL CONSTRAINT [users_version_default] DEFAULT ((1)),
  CONSTRAINT [users_pkey] PRIMARY KEY CLUSTERED ([id]),
  CONSTRAINT [users_tenant_email_key] UNIQUE NONCLUSTERED ([tenant_id], [email]),
  CONSTRAINT [users_email_check] CHECK (DATALENGTH([email]) <= 640)
);
GO
ALTER TABLE [app].[post_tags] ADD CONSTRAINT [post_tags_post_id_fkey] FOREIGN KEY ([post_id]) REFERENCES [app].[posts] ([id]) ON UPDATE NO ACTION ON DELETE NO ACTION;
GO
ALTER TABLE [app].[post_tags] ADD CONSTRAINT [post_tags_tag_id_fkey] FOREIGN KEY ([tag_id]) REFERENCES [app].[tags] ([id]) ON UPDATE NO ACTION ON DELETE NO ACTION;
GO
CREATE UNIQUE NONCLUSTERED INDEX [posts_one_published_slug] ON [app].[posts] ([slug]) WHERE ([status]='published');
GO
ALTER TABLE [app].[posts] ADD CONSTRAINT [posts_author_id_fkey] FOREIGN KEY ([author_id]) REFERENCES [app].[users] ([id]) ON UPDATE NO ACTION ON DELETE NO ACTION;
GO
ALTER TABLE [app].[posts] ADD CONSTRAINT [posts_editor_id_fkey] FOREIGN KEY ([editor_id]) REFERENCES [app].[users] ([id]) ON UPDATE NO ACTION ON DELETE NO ACTION;
GO
ALTER TABLE [app].[profiles] ADD CONSTRAINT [profiles_user_id_fkey] FOREIGN KEY ([user_id]) REFERENCES [app].[users] ([id]) ON UPDATE NO ACTION ON DELETE NO ACTION;
GO
ALTER TABLE [app].[users] ADD CONSTRAINT [users_tenant_id_fkey] FOREIGN KEY ([tenant_id]) REFERENCES [app].[tenants] ([id]) ON UPDATE NO ACTION ON DELETE NO ACTION;
GO

CREATE VIEW app.published_posts AS
  SELECT id, title, author_id, published_at
  FROM app.posts WHERE status = 'published';
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

CREATE FUNCTION app.post_score(@p_id BIGINT) RETURNS DECIMAL(38, 10) AS
BEGIN
  RETURN 0;
END;
GO
EXEC sp_addextendedproperty N'MS_Description', N'@enum(post_status)', N'SCHEMA', N'app', N'TABLE', N'posts', N'CONSTRAINT', N'posts_status_check';
EXEC sp_addextendedproperty N'MS_Description', N'@inverse-name(user_profile)', N'SCHEMA', N'app', N'TABLE', N'profiles', N'CONSTRAINT', N'profiles_user_id_fkey';
EXEC sp_addextendedproperty N'MS_Description', N'@logical-key(id)', N'SCHEMA', N'app', N'VIEW', N'published_posts';
EXEC sp_addextendedproperty N'MS_Description', N'@nonnull', N'SCHEMA', N'app', N'VIEW', N'published_posts', N'COLUMN', N'id';
EXEC sp_addextendedproperty N'MS_Description', N'@nonnull', N'SCHEMA', N'app', N'VIEW', N'published_posts', N'COLUMN', N'title';
EXEC sp_addextendedproperty N'MS_Description', N'@bound', N'SCHEMA', N'app', N'TABLE', N'users', N'COLUMN', N'tenant_id';
EXEC sp_addextendedproperty N'MS_Description', N'@write-only @sensitive', N'SCHEMA', N'app', N'TABLE', N'users', N'COLUMN', N'password_hash';
EXEC sp_addextendedproperty N'MS_Description', N'@immutable', N'SCHEMA', N'app', N'TABLE', N'users', N'COLUMN', N'created_at';
EXEC sp_addextendedproperty N'MS_Description', N'@db-updated', N'SCHEMA', N'app', N'TABLE', N'users', N'COLUMN', N'updated_at';
EXEC sp_addextendedproperty N'MS_Description', N'@version', N'SCHEMA', N'app', N'TABLE', N'users', N'COLUMN', N'version';
EXEC sp_addextendedproperty N'MS_Description', N'@public', N'SCHEMA', N'app', N'FUNCTION', N'post_score';
GO
