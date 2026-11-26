-- Injected failures for the transaction and error scenarios (guide §6.3),
-- as conformance/schemas/postgres/inject.sql does them on Postgres. Every
-- write to a table of the shop calls conformance_inject, which, while the
-- counter, an AUTO_INCREMENT that no rollback resets, has not passed the
-- planned number of failures, sleeps for the planned delay, if any, or else
-- signals the planned error number. A MySQL trigger runs for each row, not
-- each statement, so a write of several rows counts once for each.
CREATE TABLE conformance_injection (code INT NOT NULL, times INT NOT NULL, delay_us BIGINT);
CREATE TABLE conformance_injected (n BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY);

CREATE PROCEDURE conformance_inject()
BEGIN
  DECLARE c INT;
  DECLARE n INT DEFAULT 0;
  DECLARE d BIGINT;
  DECLARE CONTINUE HANDLER FOR NOT FOUND SET n = 0;
  SELECT code, times, delay_us INTO c, n, d FROM conformance_injection LIMIT 1;
  IF n > 0 THEN
    INSERT INTO conformance_injected VALUES (NULL);
    IF LAST_INSERT_ID() <= n THEN
      IF d IS NOT NULL THEN
        DO SLEEP(d / 1000000);
      ELSE
        SIGNAL SQLSTATE 'HY000' SET MESSAGE_TEXT = 'injected failure', MYSQL_ERRNO = c;
      END IF;
    END IF;
  END IF;
END;
CREATE TRIGGER conformance_inject_tenants_insert AFTER INSERT ON tenants FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_tenants_update AFTER UPDATE ON tenants FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_tenants_delete AFTER DELETE ON tenants FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_users_insert AFTER INSERT ON users FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_users_update AFTER UPDATE ON users FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_users_delete AFTER DELETE ON users FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_profiles_insert AFTER INSERT ON profiles FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_profiles_update AFTER UPDATE ON profiles FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_profiles_delete AFTER DELETE ON profiles FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_posts_insert AFTER INSERT ON posts FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_posts_update AFTER UPDATE ON posts FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_posts_delete AFTER DELETE ON posts FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_tags_insert AFTER INSERT ON tags FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_tags_update AFTER UPDATE ON tags FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_tags_delete AFTER DELETE ON tags FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_post_tags_insert AFTER INSERT ON post_tags FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_post_tags_update AFTER UPDATE ON post_tags FOR EACH ROW CALL conformance_inject();
CREATE TRIGGER conformance_inject_post_tags_delete AFTER DELETE ON post_tags FOR EACH ROW CALL conformance_inject();
