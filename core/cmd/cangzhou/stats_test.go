package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBlobBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		bad  bool
	}{
		{`[]`, 0, false}, {`[{"length":12},{"length":38}]`, 50, false},
		{`[{"length":-1}]`, 0, true}, {`[{}]`, 0, true}, {`[{"length":9223372036854775807},{"length":1}]`, 0, true},
		{`[{"length":2}] extra`, 0, true}, {`{}`, 0, true}, {`[{"length":2}`, 0, true},
	}
	for _, c := range cases {
		v, e := blobBytes(strings.NewReader(c.in))
		if (e != nil) != c.bad || (!c.bad && v != c.want) {
			t.Fatalf("blobBytes(%s)=%d,%v", c.in, v, e)
		}
	}
}
func TestRepositorySizeStateAndFailure(t *testing.T) {
	dir, exe := fakeKopia(t, `printf '[{"length":10},{"length":22}]'`)
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, ctx: context.Background(), jobs: []Config{{ID: id, Name: "t"}}, proxyErrors: map[string]string{}}
	if _, e := a.prepareJob(id); e != nil {
		t.Fatal(e)
	}
	if e := a.startSizeCheck(id, false); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	j := a.jobs[0]
	if !j.RepoSizeKnown || j.RepoSizeBytes != 32 || j.RepoSizeAt.IsZero() || j.RepoSizeAttemptAt.IsZero() || j.RepoSizeError != "" {
		t.Fatalf("%+v", j)
	}
}
func TestRepositorySizeRejectsBrokenList(t *testing.T) {
	dir, exe := fakeKopia(t, `printf '[{"length":10}'`)
	id := "0123456789abcdef"
	a := &App{dir: dir, kopia: exe, ctx: context.Background(), jobs: []Config{{ID: id, Name: "t"}}, proxyErrors: map[string]string{}}
	if _, e := a.prepareJob(id); e != nil {
		t.Fatal(e)
	}
	if e := a.startSizeCheck(id, false); e != nil {
		t.Fatal(e)
	}
	a.wg.Wait()
	j := a.jobs[0]
	if j.RepoSizeKnown || j.RepoSizeError == "" {
		t.Fatalf("%+v", j)
	}
}

func TestBlobBytesStreamsLargeList(t *testing.T) {
	const n = 350000
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"length":17}`)
	}
	b.WriteByte(']')
	if b.Len() <= 4*1024*1024 {
		t.Fatal("fixture should exceed ordinary command output limit")
	}
	got, e := blobBytes(strings.NewReader(b.String()))
	if e != nil || got != n*17 {
		t.Fatalf("got=%d err=%v", got, e)
	}
}

func TestSuccessfulBackupRefreshesRepositorySize(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			command := `if [ "$1" = "snapshot" ]; then printf '{"id":"snapshot-id"}'; else printf '[{"length":125},{"length":75}]'; fi`
			if fail {
				command = `if [ "$1" = "snapshot" ]; then printf '{"id":"snapshot-id"}'; else echo 'stats unavailable' >&2; exit 1; fi`
			}
			dir, exe := fakeKopia(t, command)
			id := "0123456789abcdef"
			a := &App{dir: dir, kopia: exe, roots: []string{dir}, ctx: context.Background(), jobs: []Config{{ID: id, Name: "test", Source: dir, RepoSizeKnown: true, RepoSizeBytes: 42, RepoSizeAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}}, proxyErrors: map[string]string{}}
			if _, e := a.prepareJob(id); e != nil {
				t.Fatal(e)
			}
			if e := a.startBackup(id); e != nil {
				t.Fatal(e)
			}
			a.wg.Wait()
			j := a.jobs[0]
			if j.LastStatus != "success" || j.RepoSizeAttemptAt.IsZero() {
				t.Fatalf("backup status lost: %+v", j)
			}
			if fail {
				if j.RepoSizeBytes != 42 || j.RepoSizeError == "" || !strings.Contains(a.message, "统计失败") {
					t.Fatalf("failed stats overwrote previous value: %+v %s", j, a.message)
				}
			} else if j.RepoSizeBytes != 200 || j.RepoSizeError != "" || j.RepoSizeAt.Year() == 2020 {
				t.Fatalf("size not refreshed: %+v", j)
			}
		})
	}
}
