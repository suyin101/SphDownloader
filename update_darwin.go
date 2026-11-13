//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var appBundle = runningAppBundle

// runningAppBundle 正在运行的 .app 路径（直接跑命令行程序时返回空）。
func runningAppBundle() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return ""
	}
	return exe[:i+4]
}

func updateDownloadPath() (string, error) {
	b := appBundle()
	if b == "" {
		return "", fmt.Errorf("不是从 .app 运行的，不能自动更新")
	}
	return filepath.Join(filepath.Dir(b), ".SphDownloader-update.zip"), nil
}

// installUpdate 解压新版 .app，把旧的挪开、新的放到原位置，再打开新的。
func installUpdate(zipFile string) error {
	b := appBundle()
	parent := filepath.Dir(b)
	tmp := filepath.Join(parent, fmt.Sprintf(".SphDownloader-new-%d", time.Now().Unix()))
	defer os.RemoveAll(tmp)
	defer os.Remove(zipFile)
	if out, err := exec.Command("ditto", "-x", "-k", zipFile, tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("解压失败：%v %s", err, out)
	}
	apps, _ := filepath.Glob(filepath.Join(tmp, "*.app"))
	if len(apps) != 1 {
		return fmt.Errorf("更新包里没找到软件")
	}
	exec.Command("xattr", "-dr", "com.apple.quarantine", apps[0]).Run()
	old := filepath.Join(parent, fmt.Sprintf(".SphDownloader-old-%d.app", time.Now().Unix()))
	if err := os.Rename(b, old); err != nil {
		return fmt.Errorf("替换失败（%v）。请把软件拖到“应用程序”或自己的文件夹里", err)
	}
	if err := os.Rename(apps[0], b); err != nil {
		os.Rename(old, b)
		return err
	}
	return relaunch(b)
}

var relaunch = func(app string) error { return exec.Command("open", "-n", app).Start() }

func cleanupOldUpdate() {
	b := appBundle()
	if b == "" {
		return
	}
	olds, _ := filepath.Glob(filepath.Join(filepath.Dir(b), ".SphDownloader-old-*.app"))
	for _, o := range olds {
		os.RemoveAll(o)
	}
	os.Remove(filepath.Join(filepath.Dir(b), ".SphDownloader-update.zip"))
}
