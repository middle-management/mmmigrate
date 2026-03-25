//go:build sqlite

package main

import (
	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/driver/sqlite"
)

var dialect mmmigrate.Dialect = sqlite.Dialect{}
