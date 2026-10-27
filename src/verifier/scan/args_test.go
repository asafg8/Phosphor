package scan

import (
	"reflect"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// A nested third-party root is classified inside its parent; a root shared by two runtimes is scanned once.
func TestRoots(t *testing.T) {
	fp := &c.Fingerprint{Runtimes: []c.Runtime{
		{ID: "cli", SAPI: "cli", CodeRoots: []c.CodeRoot{{Path: "/var/www/app", Kind: "first-party"}}},
		{ID: "fpm", SAPI: "fpm", CodeRoots: []c.CodeRoot{{Path: "/var/www/app", Kind: "first-party"},
			{Path: "/var/www/app/vendor", Kind: "third-party"}, {Path: "/srv/Other Site", Kind: "first-party"}}},
	}}
	if p := PrimaryRuntime(fp); p.ID != "fpm" {
		t.Errorf("primary %s", p.ID)
	}
	want := []Root{{Path: "/var/www/app", Kind: "first-party", Slug: "var-www-app"}, {Path: "/srv/Other Site", Kind: "first-party", Slug: "srv-other-site"}}
	if got := Roots(fp); !reflect.DeepEqual(got, want) {
		t.Errorf("roots %+v", got)
	}
}
