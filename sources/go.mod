module github.com/thec1oud/dql/sources

go 1.25

require (
	github.com/pganalyze/pg_query_go/v6 v6.2.5
	github.com/thec1oud/dql/proto v0.0.0
	google.golang.org/protobuf v1.36.6
)

replace github.com/thec1oud/dql/proto => ../proto
