package main

import "time"

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
func statusLabel(s string) string {
	switch s {
	case "running":
		return "运行中"
	case "success":
		return "成功"
	case "failed":
		return "失败"
	case "interrupted":
		return "状态未确认"
	default:
		return "暂无记录"
	}
}
