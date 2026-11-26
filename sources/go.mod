module github.com/thec1oud/dql/sources

go 1.25

require (
	github.com/go-sql-driver/mysql v1.10.2
	github.com/jackc/pgx/v5 v5.7.6
	github.com/pganalyze/pg_query_go/v6 v6.2.5
	github.com/pingcap/tidb/pkg/parser v0.0.0-20251126154744-e4e814fdc0af
	github.com/thec1oud/dql/core v0.0.0
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
	google.golang.org/protobuf v1.36.6
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/pingcap/errors v0.11.5-0.20240311024730-e056997136bb // indirect
	github.com/pingcap/failpoint v0.0.0-20240528011301-b51a646c7c86 // indirect
	github.com/pingcap/log v1.1.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace (
	github.com/thec1oud/dql/core => ../core
	github.com/thec1oud/dql/plugin => ../plugin
	github.com/thec1oud/dql/proto => ../proto
	github.com/thec1oud/dql/testkit => ../testkit
)
