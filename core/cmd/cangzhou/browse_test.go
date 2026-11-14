package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSnapshotBrowseDirectories(t *testing.T) {
	output := "drwxr-xr-x            4 2026-11-14 10:28:25 CST kab29a9a7359a0421991429181b4eb9e5  子 目录/\n" +
		"-rw-r--r--            4 2026-11-14 10:28:25 CST ff2652f8d47820026d9fe10bfd104c3b   file.txt\n"
	dirs := snapshotBrowseDirectories("parent", output)
	if len(dirs) != 1 || dirs[0].Name != "子 目录" || dirs[0].Path != "parent/子 目录" {
		t.Fatalf("unexpected directories: %+v", dirs)
	}
	for _, p := range []string{"../x", "a/../x", "/absolute", "a//b", "a\\b", "x\ny"} {
		if _, ok := snapshotBrowsePath(p); ok {
			t.Errorf("accepted invalid path %q", p)
		}
	}
}

func TestSnapshotBrowseFilesAndSpecialNames(t *testing.T) {
	output := "-rw-r--r--            4 2026-11-14 10:28:25 CST ff2652f8d47820026d9fe10bfd104c3b    leading space.txt\n" +
		"lrwxrwxrwx            0 2026-11-14 10:28:25 CST ff2652f8d47820026d9fe10bfd104c3b   link\n"
	got := snapshotBrowseEntries("folder", output)
	if len(got) != 1 || got[0].Name != " leading space.txt" || got[0].Path != "folder/ leading space.txt" || got[0].IsDir {
		t.Fatalf("unexpected files: %+v", got)
	}
}

func TestSelectedSnapshotPaths(t *testing.T) {
	got, ok := selectedSnapshotPaths([]string{"a-b", "a/x", "a", "a/y/z", "a", "other/file"})
	if !ok || !reflect.DeepEqual(got, []string{"a", "a-b", "other/file"}) {
		t.Fatalf("overlap: %v %v", got, ok)
	}
	for _, input := range [][]string{nil, {""}, {"../x"}, {"/absolute"}, {"a/../x"}, {"a\nb"}} {
		if _, ok := selectedSnapshotPaths(input); ok {
			t.Errorf("accepted invalid selection %q", input)
		}
	}
}

func TestBrowseLargeDirectoryShowsPartialResult(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "kopia")
	script := `#!/bin/sh
if [ "$1" = snapshot ]; then
  printf '%s\n' '[{"rootEntry":{"obj":"aaaaaaaaaaaaaaaaaaaa"}}]'
elif [ "$1" = list ]; then
  for arg in "$@"; do [ "$arg" != --recursive ] || exit 9; done
  printf '%s\n' 'drwxr-xr-x            4 2026-11-14 10:28:25 CST kab29a9a7359a0421991429181b4eb9e5  child/'
  awk 'BEGIN { for (i=0; i<100000; i++) print "a long listing entry with some file information" }'
else
  exit 1
fi
`
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: program, ctx: context.Background(), jobs: []Config{{ID: id}}}
	if _, err := a.prepareJob(id); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.browse(w, httptest.NewRequest("GET", "/browse?job="+id+"&id=aaaaaaaaaaaaaaaaaaaa", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "仅显示前 4 MiB") || !strings.Contains(w.Body.String(), "path=child") {
		t.Fatalf("large directory: status %d, warning %v, child %v", w.Code, strings.Contains(w.Body.String(), "仅显示前 4 MiB"), strings.Contains(w.Body.String(), "path=child"))
	}
}
