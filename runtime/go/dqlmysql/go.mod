module github.com/thec1oud/dql/runtime/go/dqlmysql

go 1.25

require (
	github.com/go-sql-driver/mysql v1.10.2
	github.com/thec1oud/dql/runtime/go v0.0.0
)

require filippo.io/edwards25519 v1.2.0 // indirect

replace github.com/thec1oud/dql/runtime/go => ../
