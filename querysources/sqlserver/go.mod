module github.com/thec1oud/dql/querysources/sqlserver

go 1.25.0

replace github.com/thec1oud/dql/proto => ../../proto

replace github.com/thec1oud/dql/plugin => ../../plugin

replace github.com/thec1oud/dql/core => ../../core

replace github.com/thec1oud/dql/testkit => ../../testkit

replace github.com/thec1oud/dql/querysources => ..

require (
	github.com/antlr4-go/antlr/v4 v4.13.1
	github.com/microsoft/go-mssqldb v1.12.0
	github.com/thec1oud/dql/core v0.0.0
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/querysources v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/text v0.41.0 // indirect
)
