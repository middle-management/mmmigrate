//go:build !postgres && !sqlite

package main

import "github.com/middle-management/mmmigrate/migrate"

var dialect migrate.Dialect
