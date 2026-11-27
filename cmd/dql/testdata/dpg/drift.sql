-- Applied after testdata/shop/schema.sql: removes the two objects that
-- schema.sql declares and the dpg source the snapshot was recorded from,
-- sources/dpgsnapshot/internal/format/testdata/shop.dpg, does not. They are
-- differences of content, not of fidelity (see sources/fidelity's parity
-- test).
DROP INDEX app.posts_one_published_slug;
COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles IS NULL;
