module github.com/thec1oud/dql/querysources/sqlite

go 1.25.0

replace github.com/thec1oud/dql/proto => ../../proto

replace github.com/thec1oud/dql/plugin => ../../plugin

replace github.com/thec1oud/dql/core => ../../core

replace github.com/thec1oud/dql/testkit => ../../testkit

replace github.com/thec1oud/dql/querysources => ..

require (
	github.com/antlr4-go/antlr/v4 v4.13.1
	github.com/thec1oud/dql/core v0.0.0
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/querysources v0.0.0
	google.golang.org/protobuf v1.36.6
)

require golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
