-- Objects beyond the shop that mysql-live reads: every column type family,
-- indexes of each kind, triggers, a procedure, a hidden function and an
-- unenforced check.
CREATE TABLE kinds (
  id     INT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'The key. @name(kind_id)',
  t8     TINYINT, u8 TINYINT UNSIGNED, flag TINYINT(1) NOT NULL DEFAULT 0,
  i16    SMALLINT, u16 SMALLINT UNSIGNED, i24 MEDIUMINT, i32 INT, u64 BIGINT UNSIGNED DEFAULT 7,
  f32    FLOAT, f64 DOUBLE DEFAULT 1.5, d DECIMAL(12, 2) DEFAULT 3.10, dn DECIMAL,
  c      CHAR(2) COLLATE utf8mb4_bin, vc VARCHAR(10) COLLATE utf8mb4_0900_as_cs, tt TINYTEXT, lt LONGTEXT,
  b      BINARY(4), vb VARBINARY(8), bl BLOB,
  dt     DATETIME(3), ts TIMESTAMP NULL, tm TIME(2), dd DATE DEFAULT '2026-01-01', y YEAR,
  j      JSON, bits BIT(3), bit1 BIT, s SET('a', 'b'), g POINT,
  e      ENUM('x', 'it''s') DEFAULT 'x',
  v      INT AS (i32 + 1) VIRTUAL,
  PRIMARY KEY (id),
  UNIQUE KEY kinds_prefix (vc(4)),
  UNIQUE KEY kinds_expr ((i32 * 2)),
  KEY kinds_desc (i16 DESC) COMMENT 'A note @ignored',
  CONSTRAINT kinds_range CHECK (i32 BETWEEN -5 AND 10),
  CONSTRAINT kinds_unenforced CHECK (i16 > 0) NOT ENFORCED,
  CONSTRAINT kinds_membership CHECK (c IN ('ab', 'cd'))
) COMMENT 'Every type. @name(kind)';

CREATE TABLE children (
  id      INT NOT NULL,
  kind_id INT UNSIGNED,
  mood    ENUM('calm', 'busy'),
  PRIMARY KEY (id),
  CONSTRAINT children_kind_fkey FOREIGN KEY (kind_id) REFERENCES kinds (id) ON DELETE CASCADE ON UPDATE SET NULL
);

CREATE TRIGGER children_bi BEFORE INSERT ON children FOR EACH ROW SET NEW.id = NEW.id;
CREATE TRIGGER children_bu BEFORE UPDATE ON children FOR EACH ROW SET NEW.id = NEW.id;

CREATE VIEW kind_names AS SELECT k.id, c.id AS child FROM kinds k JOIN children c ON c.kind_id = k.id;

CREATE PROCEDURE touch(IN a INT, OUT b INT, INOUT c INT) COMMENT '@public' SET b = a;
CREATE FUNCTION hidden_one() RETURNS INT DETERMINISTIC NO SQL COMMENT 'internal @public-not' RETURN 1;
CREATE FUNCTION pure(x INT) RETURNS INT DETERMINISTIC NO SQL COMMENT '@public' RETURN x;
CREATE FUNCTION reads_data() RETURNS INT DETERMINISTIC READS SQL DATA COMMENT '@public' RETURN 1;
