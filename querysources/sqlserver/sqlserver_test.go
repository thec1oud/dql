package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	_ "github.com/microsoft/go-mssqldb" // the sqlserver driver

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

func TestHandshake(t *testing.T) {
	hs := New().Handshake()
	if hs.GetPlugin().GetName() != "sqlserver-queries" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake = %v", hs)
	}
}

func TestInvokeRefusesOtherDialects(t *testing.T) {
	b := shop(t)
	b.Dialect = irv1.Dialect_DIALECT_POSTGRES
	_, err := New().Invoke(context.Background(), &irv1.Invoke{Bundle: b})
	if err == nil || err.Error() != "sqlserver-queries: the bundle's catalogs are DIALECT_POSTGRES, not SQL Server" {
		t.Errorf("err = %v", err)
	}
}

// TestShopGolden pins the queries of the running example on SQL Server
// (§17.3).
func TestShopGolden(t *testing.T) {
	text, err := os.ReadFile("../../conformance/schemas/sqlserver/queries.sql")
	if err != nil {
		t.Fatal(err)
	}
	res := run(t, string(text))
	if len(res.GetDiagnostics()) > 0 {
		t.Fatalf("diagnostics: %v", res.GetDiagnostics())
	}
	js, err := bundle.MarshalJSON(res.GetBundle())
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "shop.json"), js)
}

// TestDuplicatesAndSort reports a query defined twice, and sorts the
// queries by name.
func TestDuplicatesAndSort(t *testing.T) {
	res := run(t, "-- name: B :many\nSELECT id FROM app.tags;\n\n-- name: A :exec\nDELETE FROM app.tags;\n\n-- name: B :one\nSELECT 1 AS x;\n\n-- name: C bad\n")
	var names []string
	for _, q := range res.GetBundle().GetQueries().GetQueries() {
		names = append(names, q.GetName().GetStored())
	}
	if got := strings.Join(names, " "); got != "A B" {
		t.Errorf("queries %s", got)
	}
	var codes []string
	for _, d := range res.GetDiagnostics() {
		codes = append(codes, d.GetCode())
	}
	if got := strings.Join(codes, " "); !strings.Contains(got, "query-duplicate") || len(codes) != 3 {
		t.Errorf("diagnostics %s", got)
	}
}

// batches splits a script at its GO lines, as sqlcmd does.
var batches = regexp.MustCompile(`(?im)^[ \t]*GO[ \t]*$`)

// server returns a fresh database on the SQL Server DQL_TEST_SQLSERVER_URL
// names, holding the running example's schema, with the session settings
// of the sqlserver dialect. The tests fail rather than skip without one:
// they keep the package at full coverage.
func server(t *testing.T) *sql.Conn {
	t.Helper()
	base := os.Getenv("DQL_TEST_SQLSERVER_URL")
	if base == "" {
		t.Fatal("DQL_TEST_SQLSERVER_URL is not set; these tests need a SQL Server 2022 or newer, such as sqlserver://sa:password@localhost:1433")
	}
	ctx := context.Background()
	db, err := sql.Open("sqlserver", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("dql_queries_%d", os.Getpid())
	for _, s := range []string{
		"IF DB_ID('" + name + "') IS NOT NULL BEGIN ALTER DATABASE " + name + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE " + name + " END",
		"CREATE DATABASE " + name, "USE " + name, "SET ANSI_NULLS ON", "SET QUOTED_IDENTIFIER ON", "SET ARITHABORT ON",
	} {
		if _, err := c.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		c.ExecContext(ctx, "USE master")                                                      //nolint:errcheck // best effort
		c.ExecContext(ctx, "ALTER DATABASE "+name+" SET SINGLE_USER WITH ROLLBACK IMMEDIATE") //nolint:errcheck // best effort
		c.ExecContext(ctx, "DROP DATABASE "+name)                                             //nolint:errcheck // best effort
		c.Close()
	})
	ddl, err := os.ReadFile("../../conformance/schemas/sqlserver/shop.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range batches.Split(string(ddl), -1) {
		if strings.TrimSpace(b) == "" {
			continue
		}
		if _, err := c.ExecContext(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// sqlType writes the T-SQL type of a parameter of type t.
func sqlType(p *irv1.Param) string {
	if p.GetList() {
		return "nvarchar(max)"
	}
	t := p.GetType()
	switch k := t.GetKind().(type) {
	case *irv1.Type_Varchar:
		if k.Varchar.GetUnit() == irv1.LengthUnit_LENGTH_UNIT_BYTES {
			return fmt.Sprintf("varchar(%d)", k.Varchar.GetLength())
		}
		return fmt.Sprintf("nvarchar(%d)", k.Varchar.GetLength())
	case *irv1.Type_Text:
		return "nvarchar(max)"
	case *irv1.Type_LocalDatetime:
		return "datetime2"
	}
	return map[string]string{"bool": "bit", "uint8": "tinyint", "int16": "smallint", "int32": "int", "int64": "bigint",
		"float32": "real", "float64": "float", "decimal": "decimal(38, 10)", "uuid": "uniqueidentifier"}[kind(t)]
}

// family is the kind a server's type name is: char, varchar and text are
// one family, since an expression's text length is the server's to work
// out.
func family(name string) string {
	name, _, _ = strings.Cut(name, "(")
	switch name {
	case "char", "nchar", "varchar", "nvarchar", "text", "ntext":
		return "text"
	case "decimal", "numeric", "money", "smallmoney":
		return "decimal"
	case "binary", "varbinary":
		return "bytes"
	case "datetime2", "datetime", "smalldatetime":
		return "local_datetime"
	case "time":
		return "local_time"
	case "datetimeoffset":
		return "instant_with_offset"
	case "uniqueidentifier":
		return "uuid"
	}
	return map[string]string{"bit": "bool", "tinyint": "uint8", "smallint": "int16", "int": "int32", "bigint": "int64",
		"real": "float32", "float": "float64"}[name] + map[string]string{"date": "date", "xml": "xml"}[name]
}

func kindFamily(t *irv1.Type) string {
	switch k := kind(t); k {
	case "char", "varchar", "text", "json":
		return "text"
	default:
		return k
	}
}

// TestTypesMatchServer checks every typing case against SQL Server: the
// statement compiles with its parameters at the types they were given,
// and each result column has the type the server describes.
func TestTypesMatchServer(t *testing.T) {
	c := server(t)
	ctx := context.Background()
	for _, tc := range typingCases {
		body := strings.TrimPrefix(tc.body, ":exec ")
		res := run(t, "-- name: Q :exec\n"+body)
		if len(res.GetDiagnostics()) > 0 {
			t.Errorf("%s: %v", tc.body, res.GetDiagnostics())
			continue
		}
		q := res.GetBundle().GetQueries().GetQueries()[0]
		var decl []string
		for i, p := range q.GetParams() {
			decl = append(decl, fmt.Sprintf("@p%d %s", i+1, sqlType(p)))
		}
		for range q.GetScope().GetColumns() {
			decl = append(decl, fmt.Sprintf("@p%d uniqueidentifier", len(decl)+1))
		}
		rows, err := c.QueryContext(ctx, "SELECT name, system_type_name, error_message FROM sys.dm_exec_describe_first_result_set(@p1, @p2, 0)", q.GetText(), strings.Join(decl, ", "))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var name, typ, msg sql.NullString
			if err := rows.Scan(&name, &typ, &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Valid {
				t.Errorf("%s: %s", tc.body, msg.String)
			}
			got = append(got, name.String+":"+family(typ.String))
		}
		rows.Close()
		typedRes := run(t, "-- name: Q :many\n"+body)
		var want []string
		if qs := typedRes.GetBundle().GetQueries().GetQueries(); len(qs) == 1 {
			for _, f := range qs[0].GetResult().GetAnonymous().GetFields() {
				want = append(want, f.GetName()+":"+kindFamily(f.GetType()))
			}
		}
		if !strings.EqualFold(strings.Join(got, " "), strings.Join(want, " ")) {
			t.Errorf("%s\nserver %s\n typed %s", tc.body, strings.Join(got, " "), strings.Join(want, " "))
		}
	}
}
