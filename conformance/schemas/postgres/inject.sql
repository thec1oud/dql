-- Injected failures for the transaction and error scenarios (guide §6.3).
-- Every write to a table of app runs conformance.inject, which, while the
-- sequence, which no rollback resets, has not passed the planned number of
-- failures, sleeps for the planned delay, if any, or else raises the planned
-- SQLSTATE.
CREATE SCHEMA conformance;
CREATE TABLE conformance.injection (code text NOT NULL, times int NOT NULL, delay interval);
CREATE SEQUENCE conformance.injected;

CREATE FUNCTION conformance.inject() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  plan conformance.injection;
BEGIN
  SELECT * INTO plan FROM conformance.injection;
  IF plan.times > 0 AND nextval('conformance.injected') <= plan.times THEN
    IF plan.delay IS NOT NULL THEN
      PERFORM pg_sleep(extract(epoch FROM plan.delay));
    ELSE
      RAISE EXCEPTION 'injected failure' USING ERRCODE = plan.code;
    END IF;
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
