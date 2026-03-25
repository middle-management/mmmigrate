//go:build !postgres && !sqlite && !mysql

package main

import "github.com/middle-management/mmmigrate"

var dialect mmmigrate.Dialect
