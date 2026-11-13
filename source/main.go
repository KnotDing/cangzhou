package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	fmt.Printf("\n%s 藏舟目录授权诊断；此程序没有备份功能\n", time.Now().Format(time.RFC3339))
	fmt.Printf("real uid=%d gid=%d; effective uid=%d gid=%d\n", os.Getuid(), os.Getgid(), os.Geteuid(), os.Getegid())
	raw, present := os.LookupEnv("TRIM_DATA_ACCESSIBLE_PATHS")
	if !present || raw == "" {
		fmt.Println("未收到授权路径；请检查应用设置、fnOS 版本及回调。")
	}
	for _, p := range strings.Split(raw, ":") {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			fmt.Printf("忽略非绝对路径: %q\n", p)
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			fmt.Printf("%q: 无法检查: %v\n", p, err)
			continue
		}
		fmt.Printf("%q: 存在=%t 目录=%t 权限位=%s\n", p, true, fi.IsDir(), fi.Mode())
		fmt.Printf("  权限检查：读=%t 写=%t 搜索=%t（仅此目录，实际操作仍可能失败）\n", syscall.Access(p, 4) == nil, syscall.Access(p, 2) == nil, syscall.Access(p, 1) == nil)
		if fi.IsDir() {
			f, e := os.Open(p)
			if e != nil {
				fmt.Printf("  打开目录失败: %v\n", e)
			} else {
				fmt.Println("  能打开目录；不代表所有子项可读，也不代表可写")
				_ = f.Close()
			}
		}
	}
	fmt.Println("授权变化是否触发重启须在设备上验证；此结果不证明备份、恢复或权限隔离可用。")
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, os.Interrupt)
	<-c
}
