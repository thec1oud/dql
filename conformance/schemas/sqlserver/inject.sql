-- Injected stalls for the error scenarios (guide §6.3), as
-- conformance/schemas/postgres/inject.sql injects failures on Postgres.
-- Every write to a table of app runs a trigger that, while the sequence,
-- which no rollback resets, has not passed the planned number, waits for
-- the planned delay. SQL Server raises its own error numbers only for
-- what happened, and THROW takes numbers from 50000, so a trigger cannot
-- raise a deadlock or a lock timeout: only stalls are injected. Batches
-- are separated by GO lines.

CREATE SCHEMA conformance;
GO

CREATE TABLE conformance.injection (times INT NOT NULL, delay_ms INT NOT NULL);
CREATE SEQUENCE conformance.injected AS BIGINT START WITH 1;
GO

CREATE PROCEDURE conformance.inject AS
BEGIN
  SET NOCOUNT ON;
  DECLARE @times INT, @delay INT, @n BIGINT, @wait CHAR(12);
  SELECT TOP (1) @times = times, @delay = delay_ms FROM conformance.injection;
  IF @times > 0
  BEGIN
    SET @n = NEXT VALUE FOR conformance.injected;
    IF @n <= @times
    BEGIN
      SET @wait = CONVERT(CHAR(12), DATEADD(MILLISECOND, @delay, CAST('00:00' AS TIME(3))), 114);
      WAITFOR DELAY @wait;
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
