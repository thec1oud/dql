CREATE TABLE `post_tags` (
  `post_id` bigint NOT NULL,
  `tag_id` bigint NOT NULL,
  PRIMARY KEY (`post_id`, `tag_id`),
  KEY `post_tags_tag_id_fkey` (`tag_id`)
) ENGINE=InnoDB COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `posts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `author_id` bigint NOT NULL,
  `editor_id` bigint NULL,
  `title` text CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `slug` text CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci GENERATED ALWAYS AS (lower(replace(`title`,_utf8mb4' ',_utf8mb4'-'))) STORED,
  `status` enum('draft','published','archived') CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT 'draft',
  `published_at` timestamp(6) NULL,
  `featured` tinyint(1) NOT NULL DEFAULT 0 COMMENT '@bool',
  PRIMARY KEY (`id`),
  CONSTRAINT `posts_published_check` CHECK (((`published_at` is null) or (`status` <> _utf8mb4'draft'))),
  CONSTRAINT `posts_title_check` CHECK (char_length(`title`) BETWEEN 1 AND 200),
  KEY `posts_author_id_fkey` (`author_id`),
  KEY `posts_editor_id_fkey` (`editor_id`)
) ENGINE=InnoDB COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `profiles` (
  `user_id` bigint NOT NULL,
  `bio` text CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NULL,
  `birthday` date NULL,
  PRIMARY KEY (`user_id`)
) ENGINE=InnoDB COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `tags` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `tags_name_key` (`name`),
  CONSTRAINT `tags_name_check` CHECK (`name` REGEXP '(?-i)^[a-z0-9]+(-[a-z0-9]+)*$')
) ENGINE=InnoDB COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `tenants` (
  `id` char(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT (uuid()) COMMENT '@as(uuid)',
  `name` text CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `tenant_id` char(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL COMMENT '@bound @as(uuid)',
  `email` varchar(320) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `display_name` varchar(80) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NULL,
  `password_hash` text CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL COMMENT '@write-only @sensitive',
  `created_at` timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '@immutable',
  `updated_at` timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  `version` int NOT NULL DEFAULT 1 COMMENT '@version',
  PRIMARY KEY (`id`),
  UNIQUE KEY `users_tenant_email_key` (`tenant_id`, `email`),
  CONSTRAINT `users_email_check` CHECK (char_length(`email`) <= 320)
) ENGINE=InnoDB COLLATE=utf8mb4_0900_ai_ci;
ALTER TABLE `post_tags` ADD CONSTRAINT `post_tags_post_id_fkey` FOREIGN KEY (`post_id`) REFERENCES `posts` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION;
ALTER TABLE `post_tags` ADD CONSTRAINT `post_tags_tag_id_fkey` FOREIGN KEY (`tag_id`) REFERENCES `tags` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION;
ALTER TABLE `posts` ADD CONSTRAINT `posts_author_id_fkey` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION;
ALTER TABLE `posts` ADD CONSTRAINT `posts_editor_id_fkey` FOREIGN KEY (`editor_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION;
ALTER TABLE `profiles` ADD CONSTRAINT `profiles_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION;
ALTER TABLE `users` ADD CONSTRAINT `users_tenant_id_fkey` FOREIGN KEY (`tenant_id`) REFERENCES `tenants` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION;
CREATE SQL SECURITY DEFINER VIEW `published_posts` AS select `dql_test_17261_349700_1`.`posts`.`id` AS `id`,`dql_test_17261_349700_1`.`posts`.`title` AS `title`,`dql_test_17261_349700_1`.`posts`.`author_id` AS `author_id`,`dql_test_17261_349700_1`.`posts`.`published_at` AS `published_at` from `dql_test_17261_349700_1`.`posts` where (`dql_test_17261_349700_1`.`posts`.`status` = 'published');
CREATE FUNCTION `post_score`(`p_id` bigint) RETURNS decimal(65,30) DETERMINISTIC READS SQL DATA SQL SECURITY DEFINER COMMENT '@public' RETURN 0;
