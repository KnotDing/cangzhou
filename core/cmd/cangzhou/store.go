package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validJobID = regexp.MustCompile(`^(legacy|[a-f0-9]{16})$`)

func (a *App) jobDir(id string) (string, error) {
	if !validJobID.MatchString(id) {
		return "", errors.New("无效任务编号")
	}
	return filepath.Join(a.dir, "jobs", id), nil
}
func (a *App) repositoryConfigPath(j Config) (string, error) {
	dir, e := a.jobDir(j.ID)
	if e != nil {
		return "", e
	}
	if j.ConnectionID == "" {
		return filepath.Join(dir, "repository.config"), nil
	}
	if !regexp.MustCompile("^[a-f0-9]{16}$").MatchString(j.ConnectionID) {
		return "", errors.New("无效连接编号")
	}
	return filepath.Join(dir, "connections", j.ConnectionID, "repository.config"), nil
}
func (a *App) prepareJob(id string) (string, error) {
	dir, e := a.jobDir(id)
	if e != nil {
		return "", e
	}
	for _, v := range []string{"", "logs", "cache"} {
		if e = os.MkdirAll(filepath.Join(dir, v), 0700); e != nil {
			return "", e
		}
	}
	return dir, nil
}

// save is called with a.mu held; it never removes legacy files or remote data.
func (a *App) save() error {
	b, e := json.Marshal(struct {
		Version         int
		GlobalUploadKiB int
		Jobs            []Config
		DAVs            []DAVConfig
	}{1, a.globalUploadKiB, a.jobs, a.davs})
	if e != nil {
		return e
	}
	return writeFile(filepath.Join(a.dir, "state.json"), b)
}
func (a *App) find(id string) (int, Config, bool) {
	for i, j := range a.jobs {
		if j.ID == id {
			return i, j, true
		}
	}
	return -1, Config{}, false
}
func copyPrivateFile(src, dst string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	tmp, e := os.CreateTemp(filepath.Dir(dst), ".migration-*")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	if _, e = io.Copy(tmp, in); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	return os.Rename(tmp.Name(), dst)
}
func (a *App) loadJobs() error {
	stateBytes, stateErr := os.ReadFile(filepath.Join(a.dir, "state.json"))
	if stateErr == nil {
		var state struct {
			Version         int
			GlobalUploadKiB int
			Jobs            []Config
			DAVs            []DAVConfig
		}
		if e := json.Unmarshal(stateBytes, &state); e != nil {
			return fmt.Errorf("无法读取应用配置: %w", e)
		}
		if state.Version != 1 {
			return errors.New("不支持的应用配置版本")
		}
		a.globalUploadKiB = state.GlobalUploadKiB
		a.jobs = state.Jobs
		a.davs = state.DAVs
	} else if !errors.Is(stateErr, os.ErrNotExist) {
		return stateErr
	} else {
		data, e := os.ReadFile(filepath.Join(a.dir, "jobs.json"))
		if e == nil {
			if e = json.Unmarshal(data, &a.jobs); e != nil {
				return fmt.Errorf("无法读取任务列表: %w", e)
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		} else {
			old, e := os.ReadFile(filepath.Join(a.dir, "job.json"))
			if errors.Is(e, os.ErrNotExist) {
				a.jobs = []Config{}
				return nil
			}
			if e != nil {
				return e
			}
			var j Config
			if e = json.Unmarshal(old, &j); e != nil {
				return fmt.Errorf("无法读取原任务: %w", e)
			}
			if j.RepoPassword == "" {
				a.jobs = []Config{}
				return nil
			}
			j.ID = "legacy"
			j.Name = "原备份任务"
			dir, e := a.prepareJob(j.ID)
			if e != nil {
				return e
			}
			if e = copyPrivateFile(filepath.Join(a.dir, "repository.config"), filepath.Join(dir, "repository.config")); e != nil {
				return fmt.Errorf("迁移原仓库配置失败，原文件保留: %w", e)
			}
			a.jobs = []Config{j}
			if e = a.save(); e != nil {
				return fmt.Errorf("保存迁移任务失败，原文件保留: %w", e)
			}
		}
	}
	seenDAV := map[string]bool{}
	for _, d := range a.davs {
		if !validDAVID.MatchString(d.ID) || seenDAV[d.ID] || strings.TrimSpace(d.Name) == "" {
			return errors.New("WebDAV 配置包含无效或重复编号")
		}
		seenDAV[d.ID] = true
	}
	seen := map[string]bool{}
	changed := false
	for i := range a.jobs {
		if a.jobs[i].LastStatus == "running" {
			a.jobs[i].LastStatus = "interrupted"
			a.jobs[i].LastError = "上次运行中服务停止，结果未确认；请检查快照"
			changed = true
		}
	}
	for _, j := range a.jobs {
		if !validJobID.MatchString(j.ID) || seen[j.ID] || strings.TrimSpace(j.Name) == "" {
			return errors.New("任务列表包含无效或重复编号")
		}
		seen[j.ID] = true
		if _, e := a.prepareJob(j.ID); e != nil {
			return e
		}
	}
	if changed {
		return a.save()
	}
	return nil
}
