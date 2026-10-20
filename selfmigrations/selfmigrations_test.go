package selfmigrations

import (
	"testing"

	"github.com/middle-management/mmmigrate/source"
)

func TestChains(t *testing.T) {
	for _, dir := range []string{"postgres", "sqlite", "mysql"} {
		t.Run(dir, func(t *testing.T) {
			if err := source.ValidateChain(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}
