package main

import "net/http"

func (a *App) recoveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "请求方法不支持", http.StatusMethodNotAllowed)
		return
	}
	a.mu.Lock()
	busy, message := a.busy, a.visibleMessageLocked("recoveries", "")
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = recoveriesPage.Execute(w, struct {
		Restores []string
		Busy     bool
		Message  string
	}{a.restores(), busy, message})
}
func (a *App) permissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "请求方法不支持", http.StatusMethodNotAllowed)
		return
	}
	a.mu.Lock()
	roots := append([]string(nil), a.roots...)
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = permissionsPage.Execute(w, struct{ Roots []string }{roots})
}
func (a *App) tips(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "请求方法不支持", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tipsPage.Execute(w, nil)
}
