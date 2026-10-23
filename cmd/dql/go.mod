module github.com/thec1oud/dql/cmd/dql

go 1.25

require (
	github.com/thec1oud/dql/core v0.0.0
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
)

require (
	google.golang.org/protobuf v1.36.6
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/thec1oud/dql/proto => ../../proto

replace github.com/thec1oud/dql/core => ../../core

replace github.com/thec1oud/dql/plugin => ../../plugin

replace github.com/thec1oud/dql/testkit => ../../testkit
