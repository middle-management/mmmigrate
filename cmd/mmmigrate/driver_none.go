//go:build !postgres && !sqlite && !mysql && !spanner

package main

import "github.com/middle-management/mmmigrate"

var dialect mmmigrate.Dialect
