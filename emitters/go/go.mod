module github.com/thec1oud/dql/emitters/go

go 1.25

require (
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
	google.golang.org/protobuf v1.36.6
)

require github.com/google/uuid v1.6.0

replace (
	github.com/thec1oud/dql/plugin => ../../plugin
	github.com/thec1oud/dql/proto => ../../proto
	github.com/thec1oud/dql/runtime/go => ../../runtime/go
	github.com/thec1oud/dql/testkit => ../../testkit
)
