module github.com/thec1oud/dql/emitters/go

go 1.25

require (
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.6
	github.com/thec1oud/dql/runtime/go v0.0.0
	github.com/thec1oud/dql/runtime/go/dqlpgx v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

replace (
	github.com/thec1oud/dql/plugin => ../../plugin
	github.com/thec1oud/dql/proto => ../../proto
	github.com/thec1oud/dql/runtime/go => ../../runtime/go
	github.com/thec1oud/dql/runtime/go/dqlpgx => ../../runtime/go/dqlpgx
	github.com/thec1oud/dql/testkit => ../../testkit
)
