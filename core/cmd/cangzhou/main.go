package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cangzhou/internal/authority"
)

type Config struct {
	ID, Name                                        string
	DAVID, TargetPath                               string
	URL, User, WebDAVPassword, RepoPassword, Source string
	IntervalHours                                   int
	UploadLimitKiB                                  int
	effectiveUploadKiB                              int
	Next                                            time.Time
	InsecureTLS                                     bool
	ProxyUser, ProxyPassword                        string
	ProxyPort                                       int
	ConnectionID                                    string
	Paused                                          bool
	TrackingSince                                   time.Time
	RunCount                                        uint64
	LastStarted, LastFinished                       time.Time
	LastStatus, LastError                           string
	RepoSizeBytes                                   int64
	RepoSizeKnown                                   bool
	RepoSizeAt, RepoSizeAttemptAt                   time.Time
	RepoSizeError                                   string
}
type DAVConfig struct {
	ID, Name, URL, User, Password                      string
	Protocol, Host, RemotePath, PrivateKey, KnownHosts string
	Port                                               int
	InsecureTLS                                        bool
}
type Snapshot struct {
	ID               string    `json:"id"`
	IncompleteReason string    `json:"incompleteReason"`
	Start            time.Time `json:"startTime"`
	Source           struct {
		Path string `json:"path"`
	} `json:"source"`
	Root struct {
		Obj     string `json:"obj"`
		Summary struct {
			Failed int `json:"numFailed"`
		} `json:"summ"`
	} `json:"rootEntry"`
	Stats struct {
		Errors  int `json:"errorCount"`
		Ignored int `json:"ignoredErrorCount"`
	} `json:"stats"`
}
type App struct {
	dir, kopia, csrf                                 string
	roots                                            []string
	mu                                               sync.Mutex
	jobs                                             []Config
	davs                                             []DAVConfig
	busy                                             bool
	activity                                         string
	startedAt                                        time.Time
	message                                          string
	messageScope, messageJob, activeScope, activeJob string
	ctx                                              context.Context
	cancel                                           context.CancelFunc
	wg                                               sync.WaitGroup
	proxies                                          map[string]*davProxy
	proxyErrors                                      map[string]string
	progress                                         *backupProgress
	offline                                          *offlineSession
	pendingImport                                    *importDocument
	globalUploadKiB                                  int
}

var objectID = regexp.MustCompile(`^[a-z0-9]{20,100}$`)

func random(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func (a *App) command(ctx context.Context, cfg Config, args ...string) (*exec.Cmd, string, error) {
	stateDir, e := a.jobDir(cfg.ID)
	if e != nil {
		return nil, "", e
	}
	configPath, e := a.repositoryConfigPath(cfg)
	if e != nil {
		return nil, "", e
	}
	cmd := exec.CommandContext(ctx, a.kopia, args...)
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "KOPIA_") {
			env = append(env, v)
		}
	}
	cmd.Env = append(env, "KOPIA_PASSWORD="+cfg.RepoPassword, "KOPIA_WEBDAV_USERNAME="+cfg.User, "KOPIA_WEBDAV_PASSWORD="+cfg.WebDAVPassword, "KOPIA_CONFIG_PATH="+configPath, "KOPIA_LOG_DIR="+filepath.Join(stateDir, "logs"), "KOPIA_CACHE_DIRECTORY="+filepath.Join(stateDir, "cache"), "KOPIA_CHECK_FOR_UPDATES=false")

	return cmd, stateDir, nil
}
func (a *App) run(ctx context.Context, cfg Config, args ...string) ([]byte, error) {
	return a.runWithProgress(ctx, cfg, nil, args...)
}
func (a *App) runWithProgress(ctx context.Context, cfg Config, update func(string), args ...string) ([]byte, error) {
	cmd, stateDir, e := a.command(ctx, cfg, args...)
	if e != nil {
		return nil, e
	}
	if cfg.effectiveUploadKiB > 0 && len(args) >= 2 && args[0] == "snapshot" && args[1] == "create" {
		temp, e := a.limitedRepositoryConfig(cfg, cfg.effectiveUploadKiB)
		if e != nil {
			return nil, e
		}
		defer os.Remove(temp)
		for i, v := range cmd.Env {
			if strings.HasPrefix(v, "KOPIA_CONFIG_PATH=") {
				cmd.Env[i] = "KOPIA_CONFIG_PATH=" + temp
			}
		}
	}

	// Real files avoid waiting for pipe EOF if a child process inherits output.
	// The files are private and unlinked after the command returns.
	stdout, e := os.CreateTemp(stateDir, ".kopia-out-*")
	if e != nil {
		return nil, e
	}
	defer os.Remove(stdout.Name())
	defer stdout.Close()
	stderr, e := os.CreateTemp(stateDir, ".kopia-err-*")
	if e != nil {
		return nil, e
	}
	defer os.Remove(stderr.Name())
	defer stderr.Close()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	var done chan struct{}
	if update != nil {
		done = make(chan struct{})
		stopped := make(chan struct{})
		go func() { defer close(stopped); followProgress(stderr.Name(), done, update) }()
		defer func() { close(done); <-stopped }()
	}
	runErr := cmd.Run()
	out, readErr := readCommandOutput(stdout.Name(), 4*1024*1024)
	errOut, errReadErr := readCommandOutput(stderr.Name(), 128*1024)
	if runErr != nil {
		s := string(errOut) + "\n" + string(out)
		for _, secret := range []string{cfg.RepoPassword, cfg.WebDAVPassword, cfg.ProxyPassword} {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "[已隐藏]")
			}
		}
		return nil, fmt.Errorf("Kopia 执行失败: %w: %s", runErr, s)
	}
	if readErr != nil {
		return nil, fmt.Errorf("读取 Kopia 结果失败: %w", readErr)
	}
	if errReadErr != nil && !errors.Is(errReadErr, errOutputTooLarge) {
		return nil, fmt.Errorf("读取 Kopia 诊断失败: %w", errReadErr)
	}
	return out, nil

}

var errOutputTooLarge = errors.New("命令输出过大")

func readCommandOutput(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return b[:limit], errOutputTooLarge
	}
	return b, nil
}

var errBusy = errors.New("已有任务正在运行")

// Notification ownership is tracked independently from the shared activity lock.
// Callers hold a.mu when reading or changing the notification.
func (a *App) setMessageLocked(scope, job, message string) {
	a.message, a.messageScope, a.messageJob = message, scope, job
}
func (a *App) visibleMessageLocked(scope, job string) string {
	if a.messageScope != scope {
		return ""
	}
	if scope == "task" && job != "" && a.messageJob != job {
		return ""
	}
	return a.message
}
func activityScope(activity string) string {
	switch activity {
	case "更新 WebDAV 配置":
		return "remote"
	case "恢复快照", "恢复离线快照":
		return "recoveries"
	case "打开离线仓库":
		return "offline"
	case "导入设置":
		return "export"
	default:
		return "task"
	}
}

func (a *App) acquire(activity, id string) (Config, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy {
		return Config{}, errBusy
	}
	scope := activityScope(activity)
	var cfg Config
	if id != "" {
		_, j, ok := a.find(id)
		if !ok {
			return Config{}, errors.New("找不到该任务")
		}
		if reason := a.proxyErrors[id]; reason != "" && activity != "更新连接" {
			a.setMessageLocked("task", id, "任务「"+j.Name+"」："+reason)
			return Config{}, errors.New(reason)
		}
		cfg = j
		activity += "：" + j.Name
	}
	a.busy = true
	a.activeScope = scope
	a.activeJob = id
	a.activity = activity
	a.startedAt = time.Now()
	return cfg, nil
}
func (a *App) release(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(msg) > 4096 {
		msg = msg[:4096] + "…（详细信息见应用日志）"
	}
	if msg != "" {
		a.setMessageLocked(a.activeScope, a.activeJob, msg)
	}
	a.activeScope = ""
	a.activeJob = ""
	a.busy = false
	a.activity = ""
	a.startedAt = time.Time{}
	a.progress = nil

}
func (a *App) startBackup(id string) error {
	cfg, e := a.acquire("执行备份", id)
	if e != nil {
		return e
	}
	if cfg.Paused {
		a.release("任务「" + cfg.Name + "」已暂停，请先恢复任务")
		return errors.New("任务已暂停")
	}
	check, e := authority.Inspect(a.roots, cfg.Source)
	if e != nil || !check.Readable || !check.Searchable {
		a.release("任务「" + cfg.Name + "」的源目录未授权或不可读取")
		return errors.New("源目录未授权或不可读取")
	}
	a.mu.Lock()
	i, old, ok := a.find(id)
	if !ok {
		a.mu.Unlock()
		a.release("任务不存在")
		return errors.New("任务不存在")
	}
	if a.jobs[i].Paused {
		a.mu.Unlock()
		a.release("任务「" + cfg.Name + "」已暂停")
		return errors.New("任务已暂停")
	}
	now := time.Now()
	if a.jobs[i].TrackingSince.IsZero() {
		a.jobs[i].TrackingSince = now
	}
	a.jobs[i].RunCount++
	a.jobs[i].LastStarted = now
	a.jobs[i].LastFinished = time.Time{}
	a.jobs[i].LastStatus = "running"
	a.jobs[i].LastError = ""
	cfg.effectiveUploadKiB = effectiveLimit(a.globalUploadKiB, a.jobs[i].UploadLimitKiB)
	a.progress = &backupProgress{JobID: id, Started: now}
	if e := a.save(); e != nil {
		a.jobs[i] = old
		a.mu.Unlock()
		a.release("记录运行状态失败：" + e.Error())
		return e
	}
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 72*time.Hour)
		defer cancel()
		out, err := a.runWithProgress(ctx, cfg, func(line string) { a.updateBackupProgress(id, line) }, "snapshot", "create", "--json", "--progress", "--fail-fast", "--", check.Path)
		msg := "任务「" + cfg.Name + "」备份完成"
		status := "success"
		detail := ""
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				status = "interrupted"
				detail = "服务停止，运行结果未确认；请检查快照"
			} else {
				status = "failed"
				detail = err.Error()
			}
		} else {
			var snap Snapshot
			if json.Unmarshal(out, &snap) != nil || snap.ID == "" || snap.IncompleteReason != "" || snap.Root.Summary.Failed != 0 || snap.Stats.Errors != 0 || snap.Stats.Ignored != 0 {
				status = "failed"
				detail = "备份未完整完成，请检查日志"
			}
		}
		if len(detail) > 2000 {
			detail = detail[:2000] + "…"
		}
		if status != "success" {
			msg = "任务「" + cfg.Name + "」" + statusLabel(status) + "：" + detail
		}
		a.mu.Lock()
		if i, j, ok := a.find(id); ok {
			a.jobs[i].LastFinished = time.Now()
			a.jobs[i].LastStatus = status
			a.jobs[i].LastError = detail
			if j.IntervalHours > 0 && status != "interrupted" {
				a.jobs[i].Next = time.Now().Add(time.Duration(j.IntervalHours) * time.Hour)
			}
			if e := a.save(); e != nil {
				msg += "；保存运行记录失败：" + e.Error()
			}
		}
		a.mu.Unlock()
		if status == "success" {
			if e := a.refreshSizeAfterBackup(cfg); e != nil {
				msg += "；仓库大小统计失败：" + e.Error()
			}
		}
		a.release(msg)
	}()
	return nil
}
func (a *App) list(cfg Config) ([]Snapshot, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	b, e := a.run(ctx, cfg, "snapshot", "list", "--json")
	if e != nil {
		return nil, e
	}
	var s []Snapshot
	e = json.Unmarshal(b, &s)
	return s, e
}

const base = "/app/cangzhou"

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; script-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'self'")
}

type peerKey struct{}

func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		peer, ok := r.Context().Value(peerKey{}).(bool)
		admins := r.Header.Values("X-Trim-Isadmin")
		ids := r.Header.Values("X-Trim-Userid")
		if !ok || !peer || len(admins) != 1 || !strings.EqualFold(strings.TrimSpace(admins[0]), "true") || len(ids) != 1 {
			http.Error(w, "需要飞牛管理员登录", http.StatusForbidden)
			return
		}
		if _, e := strconv.ParseUint(strings.TrimSpace(ids[0]), 10, 32); e != nil {
			http.Error(w, "无效飞牛用户身份", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
func (a *App) post(r *http.Request) error {
	if r.Method != "POST" {
		return errors.New("method")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 64*1024)
	if e := r.ParseForm(); e != nil {
		return e
	}
	if r.PostForm.Get("csrf") != a.csrf {
		return errors.New("CSRF 校验失败")
	}
	return nil
}
func (a *App) snapshots(w http.ResponseWriter, r *http.Request) {
	cfg, e := a.acquire("读取快照", r.URL.Query().Get("job"))
	if e != nil {
		http.Redirect(w, r, base+"/", http.StatusSeeOther)
		return
	}
	defer a.release("")
	s, e := a.list(cfg)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = snapPage.Execute(w, struct {
		Snapshots []Snapshot
		CSRF      string
		Job       Config
	}{s, a.csrf, cfg})
}
func (a *App) browse(w http.ResponseWriter, r *http.Request) {
	cfg, e := a.acquire("浏览快照", r.URL.Query().Get("job"))
	if e != nil {
		http.Redirect(w, r, base+"/", http.StatusSeeOther)
		return
	}
	defer a.release("")
	s, e := a.list(cfg)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	id := r.URL.Query().Get("id")
	found := false
	for _, v := range s {
		if v.Root.Obj == id {
			found = true
		}
	}
	if !found || !objectID.MatchString(id) {
		http.Error(w, "无效快照", 400)
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	out, e := a.run(ctx, cfg, "list", "--recursive", id)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = browsePage.Execute(w, struct{ JobID, Text string }{cfg.ID, string(out)})
}
func (a *App) restore(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	cfg, e := a.acquire("恢复快照", r.PostForm.Get("job"))
	if e != nil {
		http.Redirect(w, r, base+"/", http.StatusSeeOther)
		return
	}
	id := r.PostForm.Get("id")
	s, e := a.list(cfg)
	found := false
	for _, v := range s {
		if v.Root.Obj == id {
			found = true
		}
	}
	if e != nil || !found || !objectID.MatchString(id) {
		a.release("恢复失败：快照无效")
		http.Error(w, "快照无效", 400)
		return
	}
	parent, e := os.MkdirTemp(filepath.Join(a.dir, "restores"), "restore-")
	if e != nil {
		a.release(e.Error())
		http.Error(w, e.Error(), 500)
		return
	}
	dest := filepath.Join(parent, "files")
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 72*time.Hour)
		defer cancel()
		_, e := a.run(ctx, cfg, "restore", "--no-overwrite-files", "--no-overwrite-directories", "--no-overwrite-symlinks", id, dest)
		if e != nil {
			a.release("恢复失败：" + e.Error())
		} else if e = writeFile(filepath.Join(parent, ".complete"), []byte("ok")); e != nil {
			a.release("恢复完成但无法保存完成标志：" + e.Error())
		} else {
			a.release("恢复完成，文件位于：" + dest)
		}
	}()
	http.Redirect(w, r, base+"/recoveries", 303)
}

// repositoryURL combines an explicitly supplied WebDAV-relative directory
// without allowing it to escape the base URL.
func repositoryURL(baseURL *url.URL, targetPath string) (string, error) {
	targetPath = strings.Trim(targetPath, "/")
	if targetPath == "" {
		return baseURL.String(), nil
	}
	segments := strings.Split(targetPath, "/")
	for i, part := range segments {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\%") {
			return "", errors.New("目标子目录必须是 WebDAV 内的相对路径，不能包含 ..、反斜杠或百分号")
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return "", errors.New("目标子目录包含无效字符")
			}
		}
		segments[i] = url.PathEscape(part)
	}
	joined, e := url.JoinPath(baseURL.String(), segments...)
	if e != nil {
		return "", e
	}
	return joined, nil
}
func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	source, e := authority.Inspect(a.roots, r.PostForm.Get("source"))
	if e != nil || !source.Readable || !source.Searchable {
		http.Error(w, "请选择有读取权限的飞牛授权目录", 400)
		return
	}
	hours, e := strconv.Atoi(r.PostForm.Get("hours"))
	if e != nil || hours < 0 || hours > 720 {
		http.Error(w, "备份间隔必须在 0–720 小时", 400)
		return
	}
	limit, e := parseUploadLimit(r.PostForm.Get("upload_limit_kib"))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	pw := r.PostForm.Get("repo_password")
	if len(pw) < 12 {
		http.Error(w, "仓库密码至少 12 位", 400)
		return
	}
	mode := r.PostForm.Get("mode")
	if mode != "create" && mode != "connect" {
		http.Error(w, "请选择新建或连接已有仓库", 400)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if name == "" || len(name) > 120 {
		http.Error(w, "请输入任务名称", 400)
		return
	}
	davID := r.PostForm.Get("dav_id")
	targetPath := strings.Trim(r.PostForm.Get("target_path"), "/")
	_, e = a.acquire("连接仓库", "")
	if e != nil {
		http.Redirect(w, r, base+"/", 303)
		return
	}
	a.mu.Lock()
	_, dav, ok := a.findDAV(davID)
	a.mu.Unlock()
	if !ok {
		a.release("")
		http.Error(w, "请选择已保存的 WebDAV 连接", 400)
		return
	}
	u, e := url.Parse(dav.URL)
	if e != nil {
		a.release("")
		http.Error(w, "WebDAV 地址无效", 400)
		return
	}
	target, e := repositoryURL(u, targetPath)
	if e != nil {
		a.release("")
		http.Error(w, e.Error(), 400)
		return
	}
	cfg := Config{ID: random(8), Name: name, DAVID: davID, TargetPath: targetPath, URL: target, User: dav.User, WebDAVPassword: dav.Password, RepoPassword: pw, Source: source.Path, IntervalHours: hours, UploadLimitKiB: limit, TrackingSince: time.Now(), InsecureTLS: dav.InsecureTLS}
	a.mu.Lock()
	a.setMessageLocked("task", "", "正在连接仓库，请稍候；连接完成后会自动更新。")
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if _, err := a.prepareJob(cfg.ID); err != nil {
			a.release("创建任务目录失败：" + err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		defer cancel()
		proxy, err := a.connectRemote(ctx, &cfg, dav, mode)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				err = errors.New("连接超时（5 分钟），请检查目标地址、网络和 WebDAV 权限")
			}
			if proxy != nil {
				proxy.Close()
			}
			if strings.Contains(err.Error(), "PROPFIND /: 404") {
				a.release("连接失败：服务器返回 404，无法读取仓库目标目录。请检查 WebDAV 地址与仓库子目录，并确认账号有访问及创建目录的权限。仓库目标：" + cfg.URL)
			} else {
				a.release("连接失败：" + err.Error() + "。如果目标目录已创建仓库，请选择连接已有仓库后重试。")
			}
			return
		}
		a.mu.Lock()
		if cfg.IntervalHours > 0 {
			cfg.Next = time.Now().Add(time.Duration(cfg.IntervalHours) * time.Hour)
		}
		a.jobs = append(a.jobs, cfg)
		err = a.save()
		if err != nil {
			a.jobs = a.jobs[:len(a.jobs)-1]
		} else if proxy != nil {
			a.proxies[cfg.ID] = proxy
		}
		a.mu.Unlock()
		if err != nil {
			if proxy != nil {
				proxy.Close()
			}
			a.release("仓库已连接，但保存任务失败：" + err.Error() + "。请修复存储权限后选择连接已有仓库。")
			return
		}

		a.release("任务「" + cfg.Name + "」已创建，可以开始备份。")
	}()
	http.Redirect(w, r, base+"/", 303)
}
func (a *App) job(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	c, e := authority.Inspect(a.roots, r.PostForm.Get("source"))
	if e != nil || !c.Readable || !c.Searchable {
		http.Error(w, "目录未授权或无法读取", 400)
		return
	}
	hours, e := strconv.Atoi(r.PostForm.Get("hours"))
	if e != nil || hours < 0 || hours > 720 {
		http.Error(w, "间隔必须在 0–720 小时", 400)
		return
	}
	limit, e := parseUploadLimit(r.PostForm.Get("upload_limit_kib"))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if name == "" || len(name) > 120 {
		http.Error(w, "请输入任务名称", 400)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy {
		http.Redirect(w, r, base+"/", 303)
		return
	}
	i, old, ok := a.find(r.PostForm.Get("job"))
	if !ok {
		http.Error(w, "找不到该任务", 404)
		return
	}
	a.jobs[i].Name = name
	a.jobs[i].Source = c.Path
	a.jobs[i].IntervalHours = hours
	a.jobs[i].UploadLimitKiB = limit
	if hours > 0 {
		a.jobs[i].Next = time.Now().Add(time.Duration(hours) * time.Hour)
	} else {
		a.jobs[i].Next = time.Time{}
	}
	if e := a.save(); e != nil {
		a.jobs[i] = old
		http.Error(w, e.Error(), 500)
		return
	}
	http.Redirect(w, r, base+"/task?job="+url.QueryEscape(r.PostForm.Get("job")), 303)
}

// pause changes future execution only. It never terminates a running backup.
func (a *App) pause(w http.ResponseWriter, r *http.Request) {
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	mode := r.PostForm.Get("action")
	if mode != "pause" && mode != "resume" {
		http.Error(w, "无效操作", 400)
		return
	}
	id := r.PostForm.Get("job")
	a.mu.Lock()
	defer a.mu.Unlock()
	i, old, ok := a.find(id)
	if !ok {
		http.Error(w, "找不到该任务", 404)
		return
	}
	paused := mode == "pause"
	if old.Paused == paused {
		http.Redirect(w, r, base+"/task?job="+url.QueryEscape(id), 303)
		return
	}
	a.jobs[i].Paused = paused
	if !paused && old.IntervalHours > 0 {
		a.jobs[i].Next = time.Now().Add(time.Duration(old.IntervalHours) * time.Hour)
	}
	if e := a.save(); e != nil {
		a.jobs[i] = old
		http.Error(w, "保存失败："+e.Error(), 500)
		return
	}
	if paused {
		a.setMessageLocked("task", old.ID, "任务「"+old.Name+"」已暂停，已开始的备份会继续。")
	} else {
		a.setMessageLocked("task", old.ID, "任务「"+old.Name+"」已恢复。")
	}
	http.Redirect(w, r, base+"/task?job="+url.QueryEscape(id), 303)
}
func (a *App) remove(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.mu.Lock()
		_, j, ok := a.find(r.URL.Query().Get("job"))
		busy := a.busy
		a.mu.Unlock()
		if busy {
			http.Redirect(w, r, base+"/", 303)
			return
		}
		if !ok {
			http.Error(w, "找不到该任务", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = removePage.Execute(w, struct{ ID, Name, URL, CSRF string }{j.ID, j.Name, j.URL, a.csrf})
		return
	}
	if e := a.post(r); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "请确认移除任务", 400)
		return
	}
	id := r.PostForm.Get("job")
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		http.Redirect(w, r, base+"/", 303)
		return
	}
	i, j, ok := a.find(id)
	if !ok {
		a.mu.Unlock()
		http.Error(w, "找不到该任务", 404)
		return
	}
	previous := append([]Config(nil), a.jobs...)
	a.jobs = append(a.jobs[:i], a.jobs[i+1:]...)
	if e := a.save(); e != nil {
		a.jobs = previous
		a.mu.Unlock()
		http.Error(w, "保存失败："+e.Error(), 500)
		return
	}
	p := a.proxies[id]
	delete(a.proxies, id)
	delete(a.proxyErrors, id)
	a.setMessageLocked("task", j.ID, "已移除任务「"+j.Name+"」，目标仓库和源文件均未删除。")
	a.mu.Unlock()
	if p != nil {
		p.Close()
	}
	http.Redirect(w, r, base+"/", 303)
}
func elapsed(start time.Time) int {
	if start.IsZero() {
		return 0
	}
	return int(time.Since(start).Seconds())
}
func sanitized(j Config) Config {
	j.RepoPassword = ""
	j.WebDAVPassword = ""
	j.ProxyPassword = ""
	j.ProxyUser = ""
	return j
}
func (a *App) home(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	needle := strings.ToLower(query)
	a.mu.Lock()
	jobs := make([]Config, 0, len(a.jobs))
	for _, j := range a.jobs {
		if needle == "" || strings.Contains(strings.ToLower(j.Name+" "+j.Source+" "+j.URL), needle) {
			jobs = append(jobs, sanitized(j))
		}
	}
	davs := make([]DAVConfig, len(a.davs))
	for i, d := range a.davs {
		davs[i] = cleanDAV(d)
	}
	data := struct {
		Roots             []string
		CSRF              string
		Busy              bool
		Message, Activity string
		Elapsed           int
		Jobs              []Config
		DAVs              []DAVConfig
		Total             int
		Query             string
		New               bool
	}{a.roots, a.csrf, a.busy, a.visibleMessageLocked("task", ""), a.activity, elapsed(a.startedAt), jobs, davs, len(a.jobs), query, r.URL.Query().Get("new") == "1" || len(a.jobs) == 0}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, data)
}
func (a *App) detail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("job")
	a.mu.Lock()
	_, j, ok := a.find(id)
	busy := a.busy
	a.mu.Unlock()
	if !ok {
		http.Error(w, "找不到该任务", 404)
		return
	}
	if !j.RepoSizeKnown && !busy && (j.RepoSizeAttemptAt.IsZero() || time.Since(j.RepoSizeAttemptAt) >= time.Hour) {
		_ = a.startSizeCheck(id, true)
	}
	a.mu.Lock()
	_, j, ok = a.find(id)
	if !ok {
		a.mu.Unlock()
		http.Error(w, "找不到该任务", 404)
		return
	}
	data := struct {
		Job      Config
		CSRF     string
		Busy     bool
		Activity string
		Elapsed  int
		Message  string
		Roots    []string
		Progress *progressView
	}{sanitized(j), a.csrf, a.busy, a.activity, elapsed(a.startedAt), a.visibleMessageLocked("task", id), a.roots, a.progressFor(id)}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = detailPage.Execute(w, data)
}
func render(w http.ResponseWriter, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = template.Must(template.New("x").Parse(`<meta charset="utf-8"><p><a href="/app/cangzhou/">返回</a></p><h1>{{.Title}}</h1><pre>{{printf "%+v" .Data}}</pre>`)).Execute(w, struct {
		Title string
		Data  any
	}{title, data})
}
func (a *App) scheduleOnce() {
	a.mu.Lock()
	var id string
	if !a.busy {
		for _, j := range a.jobs {
			if !j.Paused && j.IntervalHours > 0 && !j.Next.IsZero() && !time.Now().Before(j.Next) {
				id = j.ID
				break
			}
		}
	}
	a.mu.Unlock()
	if id != "" {
		if e := a.startBackup(id); e != nil && !errors.Is(e, errBusy) {
			a.mu.Lock()
			if i, j, ok := a.find(id); ok {
				a.jobs[i].Next = time.Now().Add(time.Duration(j.IntervalHours) * time.Hour)
				_ = a.save()
			}
			a.mu.Unlock()
		}
	}
}
func (a *App) scheduler() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.scheduleOnce()
		}
	}
}
func (a *App) restores() []string {
	entries, err := os.ReadDir(filepath.Join(a.dir, "restores"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && regexp.MustCompile(`^restore-[0-9]+$`).MatchString(e.Name()) {
			if _, err := os.Stat(filepath.Join(a.dir, "restores", e.Name(), ".complete")); err != nil {
				continue
			}
			if info, err := os.Stat(filepath.Join(a.dir, "restores", e.Name(), "files")); err == nil && info.IsDir() {
				out = append(out, e.Name())
			}
		}
	}
	return out
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !regexp.MustCompile(`^restore-[0-9]+$`).MatchString(id) {
		http.Error(w, "无效恢复目录", 400)
		return
	}
	if _, err := os.Stat(filepath.Join(a.dir, "restores", id, ".complete")); err != nil {
		http.Error(w, "恢复未完成", 404)
		return
	}
	root := filepath.Join(a.dir, "restores", id, "files")
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		http.Error(w, "恢复目录不存在", 404)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="cangzhou-restore.zip"`)
	z := zip.NewWriter(w)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			h.Name += "/"
		} else {
			h.Method = zip.Deflate
		}
		dst, err := z.CreateHeader(h)
		if err != nil || info.IsDir() {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(dst, f)
		return err
	})
	if err != nil {
		log.Printf("restore download failed: %v", err)
		return // Leave the archive incomplete so it cannot appear successful.
	}
	if err = z.Close(); err != nil {
		log.Printf("restore download close failed: %v", err)
	}
}
func main() {
	dir := os.Getenv("TRIM_PKGVAR")
	dest := os.Getenv("TRIM_APPDEST")
	if dir == "" || dest == "" {
		log.Fatal("missing fnOS paths")
	}
	_ = os.MkdirAll(dir, 0700)
	for _, d := range []string{"logs", "cache", "restores"} {
		if e := os.MkdirAll(filepath.Join(dir, d), 0700); e != nil {
			log.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{dir: dir, kopia: filepath.Join(dest, "kopia"), csrf: random(24), roots: authority.Parse(os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS")), ctx: ctx, cancel: cancel}
	a.proxies = make(map[string]*davProxy)
	a.proxyErrors = make(map[string]string)
	if e := a.loadJobs(); e != nil {
		log.Fatal(e)
	}
	for i := range a.jobs {
		j := &a.jobs[i]
		if j.InsecureTLS {
			p, _, err := newDAVProxy(j)
			if err != nil {
				a.proxyErrors[j.ID] = "本地 WebDAV 代理启动失败：" + err.Error()
				log.Printf("task %s: proxy unavailable: %v", j.ID, err)
			} else {
				a.proxies[j.ID] = p
			}
		}
	}
	m := http.NewServeMux()
	m.HandleFunc("/", a.auth(a.home))
	m.HandleFunc("/setup", a.auth(a.setup))
	m.HandleFunc("/job", a.auth(a.job))
	m.HandleFunc("/edit", a.auth(a.edit))
	m.HandleFunc("/reconnect", a.auth(a.reconnect))
	m.HandleFunc("/task", a.auth(a.detail))
	m.HandleFunc("/recoveries", a.auth(a.recoveries))
	m.HandleFunc("/permissions", a.auth(a.permissions))
	m.HandleFunc("/tips", a.auth(a.tips))
	m.HandleFunc("/limits", a.auth(a.limitsPage))
	m.HandleFunc("/limits/save", a.auth(a.saveLimits))
	m.HandleFunc("/export", a.auth(a.exportPage))
	m.HandleFunc("/export/download", a.auth(a.exportSettings))
	m.HandleFunc("/import", a.auth(a.importSettings))
	m.HandleFunc("/import/apply", a.auth(a.applyImport))
	m.HandleFunc("/offline", a.auth(a.offlinePage))
	m.HandleFunc("/offline/open", a.auth(a.offlineOpen))
	m.HandleFunc("/offline/restore", a.auth(a.offlineRestore))
	m.HandleFunc("/offline/close", a.auth(a.offlineClose))
	m.HandleFunc("/webdav", a.auth(a.webdav))
	m.HandleFunc("/webdav/save", a.auth(a.saveDAV))
	m.HandleFunc("/webdav/remove", a.auth(a.removeDAV))
	m.HandleFunc("/webdav/test", a.auth(a.testDAV))
	m.HandleFunc("/assets/dav.js", a.auth(davScript))
	m.HandleFunc("/assets/live-refresh.js", a.auth(liveRefreshScript))
	m.HandleFunc("/measure", a.auth(a.measure))
	m.HandleFunc("/remove", a.auth(a.remove))
	m.HandleFunc("/pause", a.auth(a.pause))
	m.HandleFunc("/snapshots", a.auth(a.snapshots))
	m.HandleFunc("/browse", a.auth(a.browse))
	m.HandleFunc("/restore", a.auth(a.restore))
	m.HandleFunc("/download", a.auth(a.download))
	m.HandleFunc("/backup", a.auth(func(w http.ResponseWriter, r *http.Request) {
		if e := a.post(r); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		if e := a.startBackup(r.PostForm.Get("job")); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		http.Redirect(w, r, base+"/", 303)
	}))
	sock := filepath.Join(dest, "app.sock")
	if st, err := os.Lstat(sock); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			log.Fatal("socket path exists and is not a socket")
		}
		if err = os.Remove(sock); err != nil {
			log.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		log.Fatal(err)
	}
	listener, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatal(err)
	}
	if err = os.Chmod(sock, 0666); err != nil {
		_ = listener.Close()
		log.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == base {
			http.Redirect(w, r, base+"/", http.StatusTemporaryRedirect)
			return
		}
		if strings.HasPrefix(r.URL.Path, base+"/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, base)
			r.URL.RawPath = ""
		}
		m.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, peerKey{}, trustworthyPeer(c))
	}}
	go a.scheduler()
	go func() {
		if e := srv.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			log.Fatal(e)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, os.Interrupt)
	<-ch
	cancel()
	defer func() {
		for _, p := range a.proxies {
			p.Close()
		}
	}()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	_ = srv.Shutdown(ctx2)
	a.wg.Wait()
	_, _ = io.WriteString(os.Stdout, "藏舟已停止\n")
}
