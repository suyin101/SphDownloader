package main

import (
	"os"
	"os/exec"
	"strconv"
)

// 守护进程：主程序不管怎么退出（关窗口、强制退出、崩溃），都把系统代理还原，免得断网。
func startGuard() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "--guard", strconv.Itoa(os.Getpid()))
	hideWindow(cmd)
	cmd.Start()
}

func runGuard(pidStr string) {
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return
	}
	waitForExit(pid)
	b, ok := loadProxyBackup()
	if !ok {
		return // 主程序已经正常还原过了
	}
	if owner := proxyOwner(); owner != 0 && owner != pid {
		return // 代理是别的进程（比如升级后的新版本）设的，不归我管
	}
	if proxyIsOurs() {
		savedProxy = &b
		restoreSystemProxy()
	} else {
		removeProxyBackup()
	}
}

// cleanupStaleProxy 上次异常退出、系统代理还指向我们时，还原成备份里的设置。
func cleanupStaleProxy() {
	if owner := proxyOwner(); owner != 0 && owner != os.Getpid() && processAlive(owner) {
		return
	}
	if b, ok := loadProxyBackup(); ok {
		if proxyIsOurs() {
			savedProxy = &b
			restoreSystemProxy()
		} else {
			removeProxyBackup()
		}
	}
}
