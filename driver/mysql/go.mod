module github.com/middle-management/mmmigrate/driver/mysql

go 1.26.1

require (
	github.com/go-sql-driver/mysql v1.9.2
	github.com/middle-management/mmmigrate v0.0.0-00010101000000-000000000000
)

require filippo.io/edwards25519 v1.1.0 // indirect

replace github.com/middle-management/mmmigrate => ../..
