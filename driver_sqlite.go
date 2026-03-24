//go:build sqlite

package main

import (
	"github.com/middle-management/mmmigrate/driver/sqlite"
	"github.com/middle-management/mmmigrate/migrate"
)

var dialect migrate.Dialect = sqlite.Dialect{}
