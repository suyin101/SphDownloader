//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func updateDownloadPath() (string, error) {
	exe, err := selfPath()
	if err != nil {
		return "", err
	}
	return exe + ".new", nil
}

// installUpdate Windows 允许给正在运行的 exe 改名：旧的改成 .old，新的放到原位置，再启动新的。
func installUpdate(newFile string) error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("替换失败（%v）。请把软件放到“下载”或桌面等自己的文件夹里", err)
	}
	if err := os.Rename(newFile, exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	return exec.Command(exe).Start()
}

// cleanupOldUpdate 上次升级留下的旧文件。
func cleanupOldUpdate() {
	if exe, err := selfPath(); err == nil {
		os.Remove(exe + ".old")
		os.Remove(exe + ".new")
	}
}
