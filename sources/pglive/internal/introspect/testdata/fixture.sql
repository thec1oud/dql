-- Objects the shop schema lacks, in their own schemas so the shop golden
-- stays exactly §1.3. Applied as the fixture owner role; dql_fixture_reader
-- receives grants and policies.

CREATE SCHEMA ext;
CREATE EXTENSION citext SCHEMA ext;

CREATE SCHEMA fx;
COMMENT ON SCHEMA fx IS 'fixture objects';
GRANT USAGE ON SCHEMA fx TO dql_fixture_reader;

-- A nondeterministic ICU collation (§8.9).
CREATE COLLATION fx.ci (provider = icu, locale = 'und-u-ks-level2', deterministic = false);
COMMENT ON COLLATION fx.ci IS 'case-insensitive';

CREATE TYPE fx.mood AS ENUM ('sad', 'happy');
ALTER TYPE fx.mood ADD VALUE 'neutral' BEFORE 'happy';
GRANT USAGE ON TYPE fx.mood TO dql_fixture_reader;

CREATE DOMAIN fx.code AS text COLLATE fx.ci NOT NULL DEFAULT 'none'
  CONSTRAINT code_length CHECK (char_length(VALUE) <= 8)
  CONSTRAINT code_lower CHECK (VALUE = lower(VALUE));
ALTER DOMAIN fx.code ADD CONSTRAINT code_ascii CHECK (VALUE ~ '^[ -~]*$') NOT VALID;
COMMENT ON CONSTRAINT code_length ON DOMAIN fx.code IS '@length';
COMMENT ON DOMAIN fx.code IS 'a short code';

CREATE TYPE fx.point3 AS (x float8, y float8, label text COLLATE fx.ci);
COMMENT ON COLUMN fx.point3.label IS 'attribute comment';

CREATE TYPE fx.float_range AS RANGE (subtype = float8, subtype_diff = float8mi);
COMMENT ON TYPE fx.float_range IS 'a range';

CREATE SEQUENCE fx.tickets AS integer START 100 INCREMENT 5 MINVALUE 10 MAXVALUE 1000 CACHE 3 CYCLE;

CREATE TABLE fx.rooms (
  id   integer PRIMARY KEY,
  name text NOT NULL
);

CREATE TABLE fx.bookings (
  id       bigserial PRIMARY KEY,
  room_id  integer REFERENCES fx.rooms (id) MATCH FULL ON UPDATE CASCADE ON DELETE SET NULL (room_id)
             DEFERRABLE INITIALLY DEFERRED,
  code     fx.code,
  label    text COLLATE fx.ci,
  nick     ext.citext,
  during   tstzrange NOT NULL,
  readings fx.float_range[],
  spans    fx.float_multirange,
  grid     integer[][],
  ticket   integer DEFAULT nextval('fx.tickets'),
  CONSTRAINT bookings_code_key UNIQUE NULLS NOT DISTINCT (code),
  CONSTRAINT bookings_no_overlap EXCLUDE USING gist (during WITH &&)
) WITH (fillfactor = 90);
ALTER TABLE fx.bookings ADD CONSTRAINT bookings_room_check CHECK (room_id > 0) NOT VALID;
ALTER SEQUENCE fx.tickets OWNED BY fx.bookings.ticket;
COMMENT ON CONSTRAINT bookings_no_overlap ON fx.bookings IS 'no double booking';

CREATE INDEX bookings_lower_idx ON fx.bookings (lower(during), room_id DESC) INCLUDE (label)
  WITH (fillfactor = 70);
COMMENT ON INDEX fx.bookings_lower_idx IS 'expression index';

CREATE FUNCTION fx.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$;
CREATE TRIGGER bookings_touch BEFORE UPDATE ON fx.bookings
  FOR EACH ROW EXECUTE FUNCTION fx.touch();
CREATE TRIGGER bookings_audit AFTER INSERT OR DELETE OR TRUNCATE ON fx.bookings
  FOR EACH STATEMENT EXECUTE FUNCTION fx.touch();
ALTER TABLE fx.bookings DISABLE TRIGGER bookings_audit;
COMMENT ON TRIGGER bookings_touch ON fx.bookings IS 'keeps rows fresh';

ALTER TABLE fx.bookings ENABLE ROW LEVEL SECURITY;
ALTER TABLE fx.bookings FORCE ROW LEVEL SECURITY;
CREATE POLICY bookings_read ON fx.bookings FOR SELECT TO dql_fixture_reader USING (room_id > 0);
CREATE POLICY bookings_limit ON fx.bookings AS RESTRICTIVE USING (true) WITH CHECK (room_id < 1000);
COMMENT ON POLICY bookings_read ON fx.bookings IS 'readers';

GRANT SELECT, INSERT ON fx.bookings TO dql_fixture_reader WITH GRANT OPTION;
GRANT UPDATE (label) ON fx.bookings TO dql_fixture_reader;

CREATE STATISTICS fx.bookings_stats (ndistinct, dependencies) ON room_id, code FROM fx.bookings;
CREATE STATISTICS fx.bookings_expr ON (lower(during)) FROM fx.bookings;
COMMENT ON STATISTICS fx.bookings_stats IS 'correlated';

-- Partitioning, inheritance and an unlogged table.
CREATE TABLE fx.events (
  id        bigint NOT NULL,
  happened  date NOT NULL,
  PRIMARY KEY (id, happened)
) PARTITION BY RANGE (happened);
CREATE TABLE fx.events_2026 PARTITION OF fx.events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');

CREATE UNLOGGED TABLE fx.base (a integer);
CREATE TABLE fx.derived (b integer) INHERITS (fx.base);

-- A materialized view and a view with an INSTEAD OF trigger.
CREATE MATERIALIZED VIEW fx.room_counts AS
  SELECT r.id, count(b.id) AS bookings FROM fx.rooms r LEFT JOIN fx.bookings b ON b.room_id = r.id GROUP BY r.id
  WITH NO DATA;
CREATE UNIQUE INDEX room_counts_id ON fx.room_counts (id);

CREATE VIEW fx.room_names AS SELECT id, name FROM fx.rooms;
CREATE FUNCTION fx.room_names_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$;
CREATE TRIGGER room_names_insert INSTEAD OF INSERT ON fx.room_names
  FOR EACH ROW EXECUTE FUNCTION fx.room_names_insert();

-- Routines with every argument mode, defaults, set and table results.
CREATE PROCEDURE fx.reset(INOUT n integer, IN reason text DEFAULT 'manual')
  LANGUAGE plpgsql AS $$ BEGIN n := 0; END $$;
CREATE FUNCTION fx.total(a integer, OUT total bigint, VARIADIC rest integer[] DEFAULT '{}')
  LANGUAGE sql IMMUTABLE AS $$ SELECT a::bigint $$;
CREATE FUNCTION fx.room_list(min_id integer DEFAULT 0) RETURNS TABLE (id integer, name text)
  LANGUAGE sql STABLE AS $$ SELECT id, name FROM fx.rooms WHERE id >= min_id $$;
CREATE FUNCTION fx.room_ids() RETURNS SETOF integer
  LANGUAGE sql STABLE PARALLEL SAFE SECURITY DEFINER
  BEGIN ATOMIC SELECT id FROM fx.rooms; END;
REVOKE ALL ON FUNCTION fx.room_ids() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fx.room_ids() TO dql_fixture_reader;
COMMENT ON PROCEDURE fx.reset(integer, text) IS '@public';
