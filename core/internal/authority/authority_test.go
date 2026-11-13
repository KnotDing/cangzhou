package authority

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthorization(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "code")
	sibling := filepath.Join(base, "code2")
	child := filepath.Join(root, "child")
	for _, d := range []string{root, sibling, child} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(sibling, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		roots []string
		path  string
		ok    bool
	}{
		{"root", []string{root}, root, true}, {"child", []string{root}, child, true},
		{"prefix sibling", []string{root}, sibling, false}, {"symlink escape", []string{root}, filepath.Join(root, "escape"), false},
		{"no grants", nil, child, false}, {"relative", []string{root}, "child", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Inspect(tc.roots, tc.path)
			if (err == nil) != tc.ok {
				t.Fatalf("check=%+v err=%v", c, err)
			}
			if tc.ok && (!c.Readable || !c.Searchable) {
				t.Fatalf("unexpected access %+v", c)
			}
		})
	}
	if _, err := Inspect(nil, child); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("no grants: %v", err)
	}
}
func TestParseEmptyAndRelative(t *testing.T) {
	roots := Parse(":foo:/one::/two/../two:")
	if len(roots) != 2 || roots[0] != "/one" || roots[1] != "/two" {
		t.Fatal(roots)
	}
}
