-- Injected failures for the transaction scenarios (guide §6.3). Every
-- write to a table of app runs conformance.inject, which raises the planned
-- SQLSTATE while the sequence, which no rollback resets, has not passed the
-- planned number of failures.
CREATE SCHEMA conformance;
CREATE TABLE conformance.injection (code text NOT NULL, times int NOT NULL);
CREATE SEQUENCE conformance.injected;

CREATE FUNCTION conformance.inject() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  plan conformance.injection;
BEGIN
  SELECT * INTO plan FROM conformance.injection;
  IF plan.times > 0 AND nextval('conformance.injected') <= plan.times THEN
    RAISE EXCEPTION 'injected failure' USING ERRCODE = plan.code;
  END IF;
  RETURN NULL;
END $$;

DO $$
DECLARE
  t text;
BEGIN
  FOR t IN SELECT tablename FROM pg_tables WHERE schemaname = 'app' LOOP
    EXECUTE format('CREATE TRIGGER conformance_inject AFTER INSERT OR UPDATE OR DELETE ON app.%I
      FOR EACH STATEMENT EXECUTE FUNCTION conformance.inject()', t);
  END LOOP;
END $$;
