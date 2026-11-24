module github.com/thec1oud/dql/cmd/dql

go 1.25

require (
	github.com/thec1oud/dql/core v0.0.0
	github.com/thec1oud/dql/dialects v0.0.0
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/querysources v0.0.0
	github.com/thec1oud/dql/sources v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
)

require (
	google.golang.org/protobuf v1.36.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.6
	github.com/kr/text v0.2.0 // indirect
	github.com/pganalyze/pg_query_go/v6 v6.2.5 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/thec1oud/dql/emitters/go v0.0.0
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

replace github.com/thec1oud/dql/proto => ../../proto

replace github.com/thec1oud/dql/core => ../../core

replace github.com/thec1oud/dql/plugin => ../../plugin

replace github.com/thec1oud/dql/testkit => ../../testkit

replace github.com/thec1oud/dql/sources => ../../sources

replace github.com/thec1oud/dql/emitters/go => ../../emitters/go

replace github.com/thec1oud/dql/dialects => ../../dialects

replace github.com/thec1oud/dql/querysources => ../../querysources
