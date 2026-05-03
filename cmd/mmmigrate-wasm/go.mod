module github.com/middle-management/mmmigrate/cmd/mmmigrate-wasm

go 1.26.1

require (
	github.com/middle-management/mmmigrate v0.0.0
	github.com/middle-management/mmmigrate/driver/jsdb v0.0.0
	github.com/middle-management/mmmigrate/driver/pglite v0.0.0
	github.com/middle-management/mmmigrate/driver/sqlitejs v0.0.0
)

replace (
	github.com/middle-management/mmmigrate => ../..
	github.com/middle-management/mmmigrate/driver/jsdb => ../../driver/jsdb
	github.com/middle-management/mmmigrate/driver/pglite => ../../driver/pglite
	github.com/middle-management/mmmigrate/driver/sqlitejs => ../../driver/sqlitejs
)
