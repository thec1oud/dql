-- What the shop does not exercise: alias types, the other column kinds,
-- unenforced constraints, index forms, trigger forms, view options, a view
-- of another database, and every kind of routine.

CREATE SCHEMA billing;
GO
EXEC sp_addextendedproperty 'MS_Description', 'invoices and payments', 'SCHEMA', 'billing';
CREATE TYPE billing.code FROM varchar(12) NOT NULL;
GO

CREATE TABLE billing.invoices (
  id       INT IDENTITY(100, 5) NOT NULL,
  code     billing.code,
  guid     UNIQUEIDENTIFIER ROWGUIDCOL NOT NULL CONSTRAINT invoices_guid_default DEFAULT NEWSEQUENTIALID(),
  amount   MONEY NOT NULL,
  tax      DECIMAL(9, 3),
  total    AS (amount + COALESCE(tax, 0)),
  note     VARCHAR(MAX) COLLATE Latin1_General_100_BIN2,
  stamp    ROWVERSION,
  CONSTRAINT invoices_pkey PRIMARY KEY NONCLUSTERED (id),
  CONSTRAINT invoices_amount_check CHECK (amount >= 0),
  CONSTRAINT invoices_code_key UNIQUE CLUSTERED (code DESC)
);
ALTER TABLE billing.invoices NOCHECK CONSTRAINT invoices_amount_check;
EXEC sp_addextendedproperty 'MS_Description', '@name(bill)', 'SCHEMA', 'billing', 'TABLE', 'invoices';
EXEC sp_addextendedproperty 'MS_Description', 'the key', 'SCHEMA', 'billing', 'TABLE', 'invoices', 'CONSTRAINT', 'invoices_pkey';

CREATE TABLE billing.payments (
  invoice_id INT NOT NULL,
  paid_on    DATE NOT NULL,
  amount     MONEY NOT NULL,
  CONSTRAINT payments_invoice_id_fkey FOREIGN KEY (invoice_id) REFERENCES billing.invoices (id)
    ON DELETE CASCADE ON UPDATE NO ACTION
);
ALTER TABLE billing.payments NOCHECK CONSTRAINT payments_invoice_id_fkey;
CREATE INDEX payments_by_day ON billing.payments (paid_on DESC, invoice_id) INCLUDE (amount) WHERE amount > 0;
EXEC sp_addextendedproperty 'MS_Description', '@unique', 'SCHEMA', 'billing', 'TABLE', 'payments', 'INDEX', 'payments_by_day';
GO

CREATE TRIGGER billing.payments_guard ON billing.payments INSTEAD OF INSERT AS
BEGIN
  SET NOCOUNT ON;
END;
GO
CREATE TRIGGER billing.invoices_audit ON billing.invoices AFTER INSERT, DELETE AS
BEGIN
  SET NOCOUNT ON;
END;
GO
DISABLE TRIGGER billing.invoices_audit ON billing.invoices;
GO
CREATE TRIGGER billing.payments_changed ON billing.payments AFTER UPDATE AS
BEGIN
  SET NOCOUNT ON;
END;
GO

CREATE VIEW billing.big_invoices WITH SCHEMABINDING AS
  SELECT id, amount FROM billing.invoices WHERE amount > 100
  WITH CHECK OPTION;
GO
CREATE VIEW dbo.backups AS
  SELECT backup_set_id FROM msdb.dbo.backupset;
GO
CREATE VIEW dbo.invoice_days AS
  SELECT i.id, p.paid_on FROM billing.invoices i JOIN billing.payments p ON p.invoice_id = i.id;
GO

CREATE FUNCTION billing.invoice_total(@id INT, @with_tax BIT = 1) RETURNS MONEY AS
BEGIN
  RETURN (SELECT amount FROM billing.invoices WHERE id = @id);
END;
GO
CREATE FUNCTION billing.invoices_over(@min MONEY) RETURNS TABLE AS
  RETURN SELECT id, amount FROM billing.invoices WHERE amount > @min;
GO
CREATE FUNCTION billing.codes() RETURNS @out TABLE (code billing.code, n INT) AS
BEGIN
  RETURN;
END;
GO
CREATE PROCEDURE billing.pay @invoice INT, @amount MONEY, @paid INT OUTPUT AS
BEGIN
  SET @paid = 1;
END;
GO
EXEC sp_addextendedproperty 'MS_Description', '@public', 'SCHEMA', 'billing', 'PROCEDURE', 'pay';
EXEC sp_addextendedproperty 'MS_Description', '@public', 'SCHEMA', 'billing', 'FUNCTION', 'codes';
