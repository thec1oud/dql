module github.com/thec1oud/dql/conformance

go 1.25.0

require (
	github.com/jackc/pgx/v5 v5.7.6
	github.com/microsoft/go-mssqldb v1.12.0
	github.com/thec1oud/dql/emitters/go v0.0.0
	github.com/thec1oud/dql/runtime/go v0.0.0
	github.com/thec1oud/dql/runtime/go/dqlpgx v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.66.10 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.40.1 // indirect
)

require (
	github.com/go-sql-driver/mysql v1.10.2
	github.com/google/uuid v1.6.0
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/thec1oud/dql/runtime/go/dqlmysql v0.0.0
	github.com/thec1oud/dql/runtime/go/dqlsqlite v0.0.0
	github.com/thec1oud/dql/runtime/go/dqlsqlserver v0.0.0
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace (
	github.com/thec1oud/dql/emitters/go => ../emitters/go
	github.com/thec1oud/dql/plugin => ../plugin
	github.com/thec1oud/dql/proto => ../proto
	github.com/thec1oud/dql/runtime/go => ../runtime/go
	github.com/thec1oud/dql/runtime/go/dqlpgx => ../runtime/go/dqlpgx
	github.com/thec1oud/dql/testkit => ../testkit
)

replace github.com/thec1oud/dql/runtime/go/dqlmysql => ../runtime/go/dqlmysql

replace github.com/thec1oud/dql/runtime/go/dqlsqlite => ../runtime/go/dqlsqlite

replace github.com/thec1oud/dql/runtime/go/dqlsqlserver => ../runtime/go/dqlsqlserver
