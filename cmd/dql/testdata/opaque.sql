-- §8.3: a domain over citext, an opaque type, becomes a Go type whose base
-- the go emitter's types setting must map.
CREATE EXTENSION citext;
CREATE DOMAIN app.handle AS citext;
