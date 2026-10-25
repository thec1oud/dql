module github.com/thec1oud/dql/querysources

go 1.25

require (
	github.com/pganalyze/pg_query_go/v6 v6.2.5
	github.com/thec1oud/dql/core v0.0.0
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
	google.golang.org/protobuf v1.36.6
)

replace github.com/thec1oud/dql/proto => ../proto

replace github.com/thec1oud/dql/plugin => ../plugin

replace github.com/thec1oud/dql/core => ../core

replace github.com/thec1oud/dql/testkit => ../testkit
