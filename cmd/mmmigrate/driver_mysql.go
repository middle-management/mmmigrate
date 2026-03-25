//go:build mysql

package main

import (
	"github.com/middle-management/mmmigrate"
	"github.com/middle-management/mmmigrate/driver/mysql"
)

var dialect mmmigrate.Dialect = mysql.Dialect{}
