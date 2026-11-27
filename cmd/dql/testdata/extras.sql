-- Applied after testdata/shop/schema.sql: the objects that design record
-- examples about the running example rely on but §1.3 does not declare.

-- §8.3: a citext column arrives as an opaque type.
CREATE EXTENSION citext;
ALTER TABLE app.users ADD nickname citext;

-- §6.2: a trigger travels as a typed extension.
CREATE FUNCTION app.touch_updated_at() RETURNS trigger
  LANGUAGE plpgsql AS $$ BEGIN NEW.updated_at := now(); RETURN NEW; END $$;
CREATE TRIGGER users_touch_updated_at BEFORE UPDATE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.touch_updated_at();
