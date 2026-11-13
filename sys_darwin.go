//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type proxyEntry struct {
	Enabled bool
	Server  string
	Port    string
}

type serviceState struct {
	Name   string
	Web    proxyEntry
	Secure proxyEntry
	Socks  proxyEntry
}

type proxyState struct {
	Services []serviceState
}

func run(args ...string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	return string(out), err
}

func networkServices() []string {
	out, _ := run("networksetup", "-listallnetworkservices")
	var list []string
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		list = append(list, line)
	}
	return list
}

func getEntry(kind, svc string) proxyEntry {
	out, _ := run("networksetup", "-get"+kind, svc)
	var e proxyEntry
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Enabled":
			e.Enabled = v == "Yes"
		case "Server":
			e.Server = v
		case "Port":
			e.Port = v
		}
	}
	return e
}

func readProxy() proxyState {
	var s proxyState
	for _, svc := range networkServices() {
		s.Services = append(s.Services, serviceState{
			Name:   svc,
			Web:    getEntry("webproxy", svc),
			Secure: getEntry("securewebproxy", svc),
			Socks:  getEntry("socksfirewallproxy", svc),
		})
	}
	return s
}

func setEntry(kind, svc string, e proxyEntry) {
	if e.Server != "" && e.Port != "" && e.Port != "0" {
		run("networksetup", "-set"+kind, svc, e.Server, e.Port)
	}
	state := "off"
	if e.Enabled {
		state = "on"
	}
	run("networksetup", "-set"+kind+"state", svc, state)
}

func isOurs(s proxyState) bool {
	for _, svc := range s.Services {
		if svc.Secure.Enabled && svc.Secure.Port == fmt.Sprint(proxyPort) {
			return true
		}
	}
	return false
}

// upstreamFromState 用户原来开着代理（梯子/加速器）时，其它流量继续走它。
func upstreamFromState(s proxyState) string {
	for _, svc := range s.Services {
		if svc.Secure.Enabled && svc.Secure.Server != "" && svc.Secure.Port != fmt.Sprint(proxyPort) {
			return "http://" + svc.Secure.Server + ":" + svc.Secure.Port
		}
	}
	for _, svc := range s.Services {
		if svc.Web.Enabled && svc.Web.Server != "" && svc.Web.Port != fmt.Sprint(proxyPort) {
			return "http://" + svc.Web.Server + ":" + svc.Web.Port
		}
	}
	return ""
}

var savedProxy *proxyState

func enableSystemProxy(addr string) (string, error) {
	old := readProxy()
	if len(old.Services) == 0 {
		return "", fmt.Errorf("找不到网络服务")
	}
	if isOurs(old) { // 上次没正常退出
		if b, ok := loadProxyBackup(); ok {
			old = b
		}
	}
	savedProxy = &old
	saveProxyBackup(old)
	host, port, _ := strings.Cut(addr, ":")
	ours := proxyEntry{Enabled: true, Server: host, Port: port}
	for _, svc := range old.Services {
		setEntry("webproxy", svc.Name, ours)
		setEntry("securewebproxy", svc.Name, ours)
		if svc.Socks.Enabled { // SOCKS 会抢走流量，先关掉，退出时恢复
			run("networksetup", "-setsocksfirewallproxystate", svc.Name, "off")
		}
	}
	if !isOurs(readProxy()) {
		restoreSystemProxy()
		return "", fmt.Errorf("系统代理没设置成功（可能需要管理员权限）")
	}
	return upstreamFromState(old), nil
}

func restoreSystemProxy() {
	if savedProxy == nil {
		return
	}
	for _, svc := range savedProxy.Services {
		setEntry("webproxy", svc.Name, svc.Web)
		setEntry("securewebproxy", svc.Name, svc.Secure)
		setEntry("socksfirewallproxy", svc.Name, svc.Socks)
	}
	savedProxy = nil
	removeProxyBackup()
}

func caFile() string { return filepath.Join(dataDir, "ca.crt") }

// certInstalled 用系统的方式验证证书是否已被信任。
func certInstalled() bool {
	_, err := run("security", "verify-cert", "-c", caFile(), "-p", "ssl", "-L")
	if err == nil {
		return true
	}
	_, err = run("security", "verify-cert", "-c", caFile())
	return err == nil
}

// installCert 装进“登录”钥匙串并设为信任，会弹出一次输入开机密码的窗口。
func installCert(path string) error {
	home, _ := os.UserHomeDir()
	kc := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	out, err := run("security", "add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-p", "basic", "-k", kc, path)
	if err != nil {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(out))
	}
	return nil
}

func openURL(u string)    { exec.Command("open", u).Start() }
func openFolder(p string) { exec.Command("open", p).Start() }
func setupConsole()       {}

const certHint = "第一次使用需要信任一个本机证书（只在你这台电脑上，用来读取视频号页面）。\n\n点“好”之后会弹出系统窗口，请输入开机密码并点【更新设置】。"

// alert 弹一个系统对话框（从“应用程序”双击打开时没有终端窗口）。
func alert(msg string) {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(msg)
	exec.Command("osascript", "-e", `display dialog "`+esc+`" with title "视频号下载器" buttons {"好"} default button 1 with icon note`).Run()
}

func proxyIsOurs() bool { return isOurs(readProxy()) }

func waitForExit(pid int) {
	for syscall.Kill(pid, 0) == nil {
		time.Sleep(time.Second)
	}
}

func hideWindow(cmd *exec.Cmd) {}

func revealFile(p string) { exec.Command("open", "-R", p).Start() }

// chooseFolder 弹出系统的“选择文件夹”窗口，取消返回空。
func chooseFolder(current string) string {
	script := `POSIX path of (choose folder with prompt "选择视频保存位置")`
	if current != "" {
		if _, err := os.Stat(current); err == nil {
			script = `POSIX path of (choose folder with prompt "选择视频保存位置" default location (POSIX file "` + strings.ReplaceAll(current, `"`, `\"`) + `"))`
		}
	}
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(string(out)), "/")
}

func readClipboard() string {
	out, _ := exec.Command("pbpaste").Output()
	return string(out)
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
