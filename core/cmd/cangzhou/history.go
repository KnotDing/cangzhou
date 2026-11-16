package main

import (
	"html/template"
	"net/http"
	"time"
)

const maxBackupHistory = 200

type backupRecord struct {
	Started, Finished      time.Time
	Status, Trigger, Error string
	Uploaded               int64
	UploadedKnown          bool
	RepoSizeBytes          int64
	RepoSizeKnown          bool
	RepoSizeAt             time.Time
}

type historyRow struct {
	backupRecord
	Duration string
}

func (a *App) backupHistory(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("job")
	a.mu.Lock()
	_, j, ok := a.find(id)
	if !ok {
		a.mu.Unlock()
		http.Error(w, "找不到该任务", http.StatusNotFound)
		return
	}
	rows := make([]historyRow, 0, len(j.History))
	for i := len(j.History) - 1; i >= 0; i-- {
		record := j.History[i]
		duration := "未知"
		if !record.Started.IsZero() {
			end := record.Finished
			if record.Status == "running" {
				end = time.Now()
			}
			if !end.IsZero() && !end.Before(record.Started) {
				duration = end.Sub(record.Started).Truncate(time.Second).String()
			}
		}
		rows = append(rows, historyRow{record, duration})
	}
	name := j.Name
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = historyPage.Execute(w, struct {
		ID, Name string
		Rows     []historyRow
	}{id, name, rows})
}

var historyPage = template.Must(template.New("history").Funcs(funcs).Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>历史任务 · 藏舟</title>` + styles + `</head><body><div class="shell">` + navHeader("") + `<main class="main"><a class="back" href="/app/cangzhou/task?job={{.ID}}">← 返回任务详情</a><header class="top"><div><div class="eyebrow">BACKUP HISTORY</div><h1>{{.Name}} · 历史任务</h1></div></header><section class="card"><p class="sub">仅记录此功能启用后的运行，最多保留最近 200 次。传输大小为运行时最后一次报告的上传量，可能不完整；仓库大小为成功后统计的整个仓库对象大小。</p><div class="tablewrap"><table><thead><tr><th>任务开始时间</th><th>任务耗时</th><th>运行状态</th><th>传输大小</th><th>仓库大小</th><th>触发方式</th></tr></thead><tbody>{{range .Rows}}<tr><td>{{formatTime .Started}}</td><td>{{.Duration}}</td><td>{{statusLabel .Status}}{{if .Error}}<p class="sub">{{.Error}}</p>{{end}}</td><td>{{if .UploadedKnown}}{{humanBytes .Uploaded}}{{else}}未知{{end}}</td><td>{{if .RepoSizeKnown}}{{humanBytes .RepoSizeBytes}}<p class="sub">统计于 {{formatTime .RepoSizeAt}}</p>{{else}}未知{{end}}</td><td>{{if eq .Trigger "manual"}}手动{{else if eq .Trigger "scheduled"}}计划{{else}}未知{{end}}</td></tr>{{else}}<tr><td colspan="6"><div class="empty">暂无运行记录</div></td></tr>{{end}}</tbody></table></div></section></main></div></body></html>`))
