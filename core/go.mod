module github.com/thec1oud/dql/core

go 1.25

require (
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
	google.golang.org/protobuf v1.36.6
)

replace github.com/thec1oud/dql/proto => ../proto

replace github.com/thec1oud/dql/testkit => ../testkit
