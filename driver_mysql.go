//go:build mysql

package main

import (
	"github.com/middle-management/mmmigrate/driver/mysql"
	"github.com/middle-management/mmmigrate/migrate"
)

var dialect migrate.Dialect = mysql.Dialect{}
