package main

import (
	"net/http"
	"net/url"
)

// A run token prevents a stale page from cancelling a later run of the same job.
func (a *App) cancelBackup(w http.ResponseWriter, r *http.Request) {
	if err := a.post(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, run := r.PostForm.Get("job"), r.PostForm.Get("run")
	a.mu.Lock()
	if !a.busy || a.activeJob != id || a.backupCancel == nil || run == "" || run != a.backupRunID {
		a.mu.Unlock()
		http.Error(w, "备份已结束或任务已变化，请刷新页面", http.StatusConflict)
		return
	}
	if !a.backupCancelRequested {
		a.backupCancelRequested = true
		a.setMessageLocked("task", id, "正在取消本次备份，请等待进程退出。")
		a.backupCancel()
	}
	a.mu.Unlock()
	http.Redirect(w, r, base+"/task?job="+url.QueryEscape(id), http.StatusSeeOther)
}
