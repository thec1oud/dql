-- Everything sqlite-live reads that the shop does not use: tables that are
-- not STRICT, WITHOUT ROWID, constraints without names, collations,
-- virtual generated columns, partial and expression indexes, keys that
-- reference a primary key implicitly, and annotations sqlite-live reports.

CREATE TABLE loose ( -- @name(Loose)
  id      INTEGER PRIMARY KEY DESC,
  n       INT DEFAULT 7,
  label   VARCHAR(40) COLLATE NOCASE DEFAULT 'x' UNIQUE,
  padded  CHAR(8) COLLATE RTRIM,
  price   NUMERIC(10, 2) DEFAULT 1.5,
  ratio   DOUBLE DEFAULT -2.5,
  blob,
  made    TEXT DEFAULT CURRENT_TIMESTAMP,
  twice   INT GENERATED ALWAYS AS (n * 2),
  CHECK (n > 0),
  CHECK (n < 100),
  CHECK (length(label) <= 40)
);

CREATE TABLE pairs (
  a TEXT,
  b TEXT,
  note ANY, -- @name(oops
  PRIMARY KEY (a, b),
  UNIQUE (b, a) -- @deprecated
) STRICT, WITHOUT ROWID;

CREATE TABLE refs (
  id      INTEGER NOT NULL PRIMARY KEY,
  loose   INTEGER REFERENCES loose ON DELETE CASCADE ON UPDATE SET NULL,
  pa      TEXT,
  pb      TEXT,
  deleted INTEGER,
  ghost   INTEGER REFERENCES Ghosts (id),
  FOREIGN KEY (pa, pb) REFERENCES pairs (a, b), /* @name(Pair) */
  CONSTRAINT refs_custom_key UNIQUE (pb)
) STRICT;

CREATE UNIQUE INDEX refs_live ON refs (pa) WHERE deleted IS NULL AND NOT deleted;
CREATE UNIQUE INDEX refs_lower ON refs (lower(pa), pb DESC);
CREATE INDEX refs_pb ON refs (pb COLLATE NOCASE);

CREATE TRIGGER refs_noop AFTER INSERT ON refs BEGIN SELECT 1; END;
CREATE TRIGGER refs_also AFTER DELETE ON refs BEGIN SELECT 1; END;

CREATE VIEW recent AS
  WITH r AS (SELECT * FROM refs)
  SELECT r.id, l.label FROM r JOIN loose AS l ON l.id = r.loose;

CREATE TABLE bare (x);
