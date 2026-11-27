-- Injected failures and stalls for the transaction and error scenarios
-- (guide §6.3), as conformance/schemas/postgres/inject.sql injects them on
-- Postgres. Every write to a table of app runs a trigger that, while the
-- sequence, which no rollback resets, has not passed the planned number,
-- waits for the planned delay, or else meets a real failure. SQL Server
-- raises its own error numbers only for what happened, and THROW takes
-- numbers from 50000, so the failures are real ones, against a second
-- session the harness runs (conformance/harness/go/sqlserver.go):
--
--   - deadlock: the write's n-th injection locks blocker row (n, 'a') and
--     then (n, 'b'), which the harness's session holds while it waits for
--     (n, 'a'). The write runs at low deadlock priority, so SQL Server makes
--     it the victim, with error 1205.
--   - lock_timeout: the write waits for row (0, 'b') with LOCK_TIMEOUT 0
--     while the harness's session holds it, which fails with error 1222.
--
-- A write first waits, at most 10 seconds, until the harness's session
-- holds the row it will wait for. The harness's sessions set CONTEXT_INFO
-- to 0x64716C, so that the next plan can end them. Batches are separated by
-- GO lines.

CREATE SCHEMA conformance;
GO

CREATE TABLE conformance.injection (kind VARCHAR(16) NOT NULL, times INT NOT NULL, delay_ms INT NOT NULL);
CREATE TABLE conformance.blocker (n INT NOT NULL, side CHAR(1) NOT NULL, hits INT NOT NULL CONSTRAINT blocker_hits_default DEFAULT 0,
  CONSTRAINT blocker_pkey PRIMARY KEY (n, side));
-- Rows for injections of up to nine failures each.
INSERT INTO conformance.blocker (n, side)
  SELECT v.n, s.side FROM (VALUES (0), (1), (2), (3), (4), (5), (6), (7), (8), (9)) v (n) CROSS JOIN (VALUES ('a'), ('b')) s (side);
CREATE SEQUENCE conformance.injected AS BIGINT START WITH 1;
GO

-- held waits until another session holds blocker row (@n, @side): a read
-- that skips locked rows no longer finds it.
CREATE PROCEDURE conformance.held @n INT, @side CHAR(1) AS
BEGIN
  SET NOCOUNT ON;
  DECLARE @tries INT = 0;
  WHILE @tries < 1000 AND EXISTS (SELECT 1 FROM conformance.blocker WITH (READCOMMITTED, READPAST) WHERE n = @n AND side = @side)
  BEGIN
    WAITFOR DELAY '00:00:00.010';
    SET @tries += 1;
  END
END;
GO

CREATE PROCEDURE conformance.inject AS
BEGIN
  SET NOCOUNT ON;
  DECLARE @kind VARCHAR(16), @times INT, @delay INT, @n BIGINT, @wait CHAR(12);
  SELECT TOP (1) @kind = kind, @times = times, @delay = delay_ms FROM conformance.injection;
  IF @times > 0
  BEGIN
    SET @n = NEXT VALUE FOR conformance.injected;
    IF @n <= @times
    BEGIN
      IF @kind = 'deadlock'
      BEGIN
        SET DEADLOCK_PRIORITY LOW;
        EXEC conformance.held @n, 'b';
        UPDATE conformance.blocker SET hits += 1 WHERE n = @n AND side = 'a';
        UPDATE conformance.blocker SET hits += 1 WHERE n = @n AND side = 'b';
      END
      ELSE IF @kind = 'lock_timeout'
      BEGIN
        EXEC conformance.held 0, 'b';
        SET LOCK_TIMEOUT 0;
        UPDATE conformance.blocker SET hits += 1 WHERE n = 0 AND side = 'b';
      END
      ELSE
      BEGIN
        SET @wait = CONVERT(CHAR(12), DATEADD(MILLISECOND, @delay, CAST('00:00' AS TIME(3))), 114);
        WAITFOR DELAY @wait;
      END
    END
  END
END;
GO

DECLARE @t sysname, @sql NVARCHAR(MAX);
DECLARE tables CURSOR LOCAL FAST_FORWARD FOR
  SELECT t.name FROM sys.tables t WHERE SCHEMA_NAME(t.schema_id) = 'app';
OPEN tables;
FETCH NEXT FROM tables INTO @t;
WHILE @@FETCH_STATUS = 0
BEGIN
  SET @sql = N'CREATE TRIGGER app.' + QUOTENAME(@t + N'_conformance_inject') + N' ON app.' + QUOTENAME(@t) +
    N' AFTER INSERT, UPDATE, DELETE AS BEGIN SET NOCOUNT ON; EXEC conformance.inject; END;';
  EXEC sp_executesql @sql;
  FETCH NEXT FROM tables INTO @t;
END;
CLOSE tables;
DEALLOCATE tables;
GO
