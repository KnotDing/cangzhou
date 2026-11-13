package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type exportDAV struct {
	ID, Name, URL, User                    string
	Protocol, Host, RemotePath, KnownHosts string
	Port                                   int
	PrivateKey                             string `json:",omitempty"`
	InsecureTLS                            bool
	Password                               string `json:",omitempty"`
}
type exportJob struct {
	ID, Name, DAVID, TargetPath, URL, User, Source string
	IntervalHours                                  int
	UploadLimitKiB                                 int
	Paused, InsecureTLS                            bool
	WebDAVPassword                                 string `json:",omitempty"`
	RepoPassword                                   string `json:",omitempty"`
}

func (a *App) exportPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	a.mu.Lock()
	data := struct {
		CSRF, Message   string
		Busy, Importing bool
	}{a.csrf, a.visibleMessageLocked("export", ""), a.busy, a.busy && a.activeScope == "export"}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingsExportPage.Execute(w, data)
}
func (a *App) exportSettings(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	include := r.PostForm.Get("include_secrets") == "yes"
	data := struct {
		Version           int
		ExportedAt        time.Time
		IncludesPasswords bool
		GlobalUploadKiB   int
		DAVs              []exportDAV
		Jobs              []exportJob
	}{Version: 1, ExportedAt: time.Now(), IncludesPasswords: include, DAVs: []exportDAV{}, Jobs: []exportJob{}}
	a.mu.Lock()
	data.GlobalUploadKiB = a.globalUploadKiB
	for _, d := range a.davs {
		x := exportDAV{ID: d.ID, Name: d.Name, URL: d.URL, User: d.User, InsecureTLS: d.InsecureTLS, Protocol: d.Protocol, Host: d.Host, Port: d.Port, RemotePath: d.RemotePath, KnownHosts: d.KnownHosts}
		if include {
			x.Password = d.Password
			x.PrivateKey = d.PrivateKey
		}
		data.DAVs = append(data.DAVs, x)
	}
	for _, j := range a.jobs {
		x := exportJob{ID: j.ID, Name: j.Name, DAVID: j.DAVID, TargetPath: j.TargetPath, URL: j.URL, User: j.User, Source: j.Source, IntervalHours: j.IntervalHours, UploadLimitKiB: j.UploadLimitKiB, Paused: j.Paused, InsecureTLS: j.InsecureTLS}
		if include {
			x.RepoPassword = j.RepoPassword
			x.WebDAVPassword = j.WebDAVPassword
		}
		data.Jobs = append(data.Jobs, x)
	}
	a.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="cangzhou-settings.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(data)
}
