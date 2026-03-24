//go:build postgres

package main

import (
	"github.com/middle-management/mmmigrate/driver/postgres"
	"github.com/middle-management/mmmigrate/migrate"
)

var dialect migrate.Dialect = postgres.Dialect{}
