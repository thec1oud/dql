-- Objects of design record examples that the shop schema of §1.3 lacks,
-- applied on top of it in a database of their own so the shop golden stays
-- exactly §1.3.

-- §6.2: a trigger, which travels as an extension.
CREATE FUNCTION app.touch_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at := now(); RETURN NEW; END $$;
CREATE TRIGGER users_touch_updated_at BEFORE UPDATE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.touch_updated_at();

-- §8.3: a column of an extension's type, which is opaque.
CREATE EXTENSION citext SCHEMA public;
ALTER TABLE app.users ADD nickname citext;
