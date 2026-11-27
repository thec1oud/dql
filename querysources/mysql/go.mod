module github.com/thec1oud/dql/querysources/mysql

go 1.25.0

replace github.com/thec1oud/dql/proto => ../../proto

replace github.com/thec1oud/dql/plugin => ../../plugin

replace github.com/thec1oud/dql/core => ../../core

replace github.com/thec1oud/dql/testkit => ../../testkit

replace github.com/thec1oud/dql/querysources => ..

require (
	github.com/pingcap/tidb/pkg/parser v0.0.0-20261117043050-b2df425a2a69
	github.com/thec1oud/dql/core v0.0.0
	github.com/thec1oud/dql/plugin v0.0.0
	github.com/thec1oud/dql/proto v0.0.0
	github.com/thec1oud/dql/querysources v0.0.0
	github.com/thec1oud/dql/testkit v0.0.0
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/pingcap/errors v0.11.5-0.20240311024730-e056997136bb // indirect
	github.com/pingcap/failpoint v0.0.0-20240528011301-b51a646c7c86 // indirect
	github.com/pingcap/log v1.1.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)
