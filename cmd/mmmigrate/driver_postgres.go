//go:build postgres

package main

import (
	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/driver/postgres"
)

var dialect mmmigrate.Dialect = postgres.Dialect{}
