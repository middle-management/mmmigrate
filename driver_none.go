//go:build !postgres && !sqlite && !mysql

package main

import "github.com/middle-management/mmmigrate/migrate"

var dialect migrate.Dialect
