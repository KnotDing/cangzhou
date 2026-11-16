package main

import (
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type backupProgress struct {
	JobID                                     string
	Started                                   time.Time
	Processed, Total, Uploaded                int64
	KnownProcessed, KnownTotal, KnownUploaded bool
	Remaining                                 string
}
type progressView struct {
	Started            time.Time
	Processed, Total   string
	Elapsed, Remaining string
	Percent            int
	KnownTotal         bool
}

var progressCounts = regexp.MustCompile(`hashed \(([0-9.]+)\s+([kKMGTPE]?i?B)\),\s*[0-9]+ cached \(([0-9.]+)\s+([kKMGTPE]?i?B)\)`)
var progressUploaded = regexp.MustCompile(`uploaded\s+([0-9.]+)\s+([kKMGTPE]?i?B)`)
var progressEstimate = regexp.MustCompile(`estimated\s+([0-9.]+)\s+([kKMGTPE]?i?B)\s+\([^)]*\)\s+([^,\r\n]+?)\s+left`)

func progressBytes(value, unit string) (int64, bool) {
	v, e := strconv.ParseFloat(value, 64)
	if e != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	u := strings.ToUpper(unit)
	base := 1000.0
	if strings.Contains(u, "I") {
		base = 1024
	}
	u = strings.ReplaceAll(u, "I", "")
	power := strings.Index("BKMGTPE", string(u[0]))
	if power < 0 {
		return 0, false
	}
	v *= math.Pow(base, float64(power))
	if v >= math.MaxInt64 {
		return 0, false
	}
	return int64(v), true
}
func parseProgress(line string, p *backupProgress) bool {
	m := progressCounts.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	hashed, ok := progressBytes(m[1], m[2])
	if !ok {
		return false
	}
	cached, ok := progressBytes(m[3], m[4])
	if !ok || hashed > math.MaxInt64-cached {
		return false
	}
	if upload := progressUploaded.FindStringSubmatch(line); upload != nil {
		if n, ok := progressBytes(upload[1], upload[2]); ok {
			p.Uploaded, p.KnownUploaded = n, true
		}
	}
	p.Processed = hashed + cached
	p.KnownProcessed = true
	p.KnownTotal = false
	p.Remaining = ""
	if m = progressEstimate.FindStringSubmatch(line); m != nil {
		if total, ok := progressBytes(m[1], m[2]); ok && total > 0 && p.Processed <= total {
			p.Total = total
			p.KnownTotal = true
			p.Remaining = m[3]
		}
	}
	return true
}
func (a *App) updateBackupProgress(id, line string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.progress != nil && a.progress.JobID == id {
		parseProgress(line, a.progress)
	}
}

// Called with a.mu held.
func (a *App) progressFor(id string) *progressView {
	p := a.progress
	if p == nil || p.JobID != id {
		return nil
	}
	v := &progressView{Started: p.Started, Processed: "等待数据", Total: "估算中", Remaining: "估算中", Elapsed: time.Since(p.Started).Truncate(time.Second).String(), KnownTotal: p.KnownTotal}
	if p.KnownProcessed {
		v.Processed = humanBytes(p.Processed)
	}
	if p.KnownTotal {
		v.Total = humanBytes(p.Total)
		v.Percent = int(float64(p.Processed) * 100 / float64(p.Total))
		if v.Percent > 99 {
			v.Percent = 99
		}
		if p.Remaining != "" {
			v.Remaining = p.Remaining
		}
	}
	return v
}
func followProgress(path string, done <-chan struct{}, update func(string)) {
	f, e := os.Open(path)
	if e != nil {
		return
	}
	defer f.Close()
	var pending string
	read := func() {
		buf := make([]byte, 8192)
		for {
			n, e := f.Read(buf)
			if n > 0 {
				pending += string(buf[:n])
				if len(pending) > 65536 {
					pending = pending[len(pending)-65536:]
				}
				for {
					i := strings.IndexAny(pending, "\r\n")
					if i < 0 {
						break
					}
					line := pending[:i]
					pending = pending[i+1:]
					update(line)
				}
			}
			if e != nil {
				if e == io.EOF {
					return
				}
				return
			}
			if n == 0 {
				return
			}
		}
	}
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			read()
		case <-done:
			read()
			if pending != "" {
				update(pending)
			}
			return
		}
	}
}
