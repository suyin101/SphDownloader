//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const inetKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type proxyState struct {
	Enable   uint32
	Server   string
	Override string
	AutoURL  string
}

func readProxy() proxyState {
	var s proxyState
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE)
	if err != nil {
		return s
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		s.Enable = uint32(v)
	}
	s.Server, _, _ = k.GetStringValue("ProxyServer")
	s.Override, _, _ = k.GetStringValue("ProxyOverride")
	s.AutoURL, _, _ = k.GetStringValue("AutoConfigURL")
	return s
}

func writeProxy(s proxyState) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	k.SetDWordValue("ProxyEnable", s.Enable)
	k.SetStringValue("ProxyServer", s.Server)
	k.SetStringValue("ProxyOverride", s.Override)
	if s.AutoURL == "" {
		k.DeleteValue("AutoConfigURL")
	} else {
		k.SetStringValue("AutoConfigURL", s.AutoURL)
	}
	refreshInet()
	return nil
}

func refreshInet() {
	wininet := windows.NewLazySystemDLL("wininet.dll")
	set := wininet.NewProc("InternetSetOptionW")
	set.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	set.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}

// upstreamFromState 如果用户原本开着别的代理（比如加速器），返回它，好让我们转发过去。
func upstreamFromState(s proxyState) string {
	if s.Enable == 0 || s.Server == "" || strings.Contains(s.Server, fmt.Sprint(proxyPort)) {
		return ""
	}
	srv := s.Server
	if strings.Contains(srv, "=") { // 形如 http=1.2.3.4:80;https=...
		for _, part := range strings.Split(srv, ";") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 && (kv[0] == "https" || kv[0] == "http") {
				srv = kv[1]
				if kv[0] == "https" {
					break
				}
			}
		}
	}
	if !strings.Contains(srv, "://") {
		srv = "http://" + srv
	}
	return srv
}

var savedProxy *proxyState

func enableSystemProxy(addr string) (string, error) {
	old := readProxy()
	// 上次没正常退出时，注册表里还是我们自己的代理，不能当成“原来的设置”
	if strings.Contains(old.Server, fmt.Sprint(proxyPort)) {
		if b, ok := loadProxyBackup(); ok {
			old = b
		} else {
			old = proxyState{Override: old.Override}
		}
	}
	savedProxy = &old
	saveProxyBackup(old)
	err := writeProxy(proxyState{
		Enable:   1,
		Server:   addr,
		Override: "localhost;127.*;10.*;172.16.*;192.168.*;<local>",
	})
	return upstreamFromState(old), err
}

func restoreSystemProxy() {
	if savedProxy == nil {
		return
	}
	writeProxy(*savedProxy)
	savedProxy = nil
	removeProxyBackup()
}

// certInstalled 证书在“本地计算机”或“当前用户”的受信任根证书里都算装好了。
// 微信内置浏览器有时只认本地计算机的，所以优先装到本地计算机。
func certInstalled() bool { return certInStore(false) || certInStore(true) }

func certInMachineStore() bool { return certInStore(false) }

func certInStore(user bool) bool {
	args := []string{"-store", "Root", caName}
	if user {
		args = append([]string{"-user"}, args...)
	}
	cmd := exec.Command("certutil", args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return err == nil && strings.Contains(string(out), caName)
}

// installCert 先装到“本地计算机”（会弹出一次“用户账户控制”，点“是”）；
// 用户点了“否”就退而装到“当前用户”（会弹“安全警告”，点“是”）。
func installCert(path string) error {
	ps := "Start-Process -Verb RunAs -Wait -WindowStyle Hidden -FilePath certutil -ArgumentList '-addstore','Root','" + strings.ReplaceAll(path, "'", "''") + "'"
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	hideWindow(cmd)
	cmd.Run()
	if certInMachineStore() {
		return nil
	}
	cmd = exec.Command("certutil", "-user", "-addstore", "Root", path)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v %s", err, out)
	}
	return nil
}

func openURL(u string) { exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start() }

func openFolder(p string) { exec.Command("explorer", p).Start() }

func setupConsole() {
	k32 := windows.NewLazySystemDLL("kernel32.dll")
	k32.NewProc("SetConsoleOutputCP").Call(65001)
	k32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(unsafePtr("视频号下载器 - 关掉这个窗口就会退出"))))
	// 点窗口右上角 × 关闭时，先把系统代理还原
	handler := syscall.NewCallback(func(ev uint32) uintptr {
		restoreSystemProxy()
		os.Exit(0)
		return 1
	})
	k32.NewProc("SetConsoleCtrlHandler").Call(handler, 1)
}

func unsafePtr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

const certHint = "第一次使用需要安装一个本机证书（只在你这台电脑上，用来读取视频号页面）。\n\n点“确定”后会弹出“用户账户控制”，请点【是】。"

// alert 弹一个 Windows 对话框（软件没有黑色控制台窗口）。
func alert(msg string) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	t, _ := windows.UTF16PtrFromString("视频号下载器")
	m, _ := windows.UTF16PtrFromString(msg)
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40|0x40000) // 信息图标 + 置顶
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

func proxyIsOurs() bool { return strings.Contains(readProxy().Server, fmt.Sprint(proxyPort)) }

func waitForExit(pid int) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	windows.WaitForSingleObject(h, windows.INFINITE)
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

func revealFile(p string) { exec.Command("explorer", "/select,", p).Start() }

func chooseFolder(current string) string {
	ps := `Add-Type -AssemblyName System.Windows.Forms;` +
		`$f=New-Object System.Windows.Forms.FolderBrowserDialog;$f.Description='选择视频保存位置';` +
		`$f.SelectedPath='` + strings.ReplaceAll(current, "'", "''") + `';` +
		`if($f.ShowDialog() -eq 'OK'){[Console]::OutputEncoding=[Text.Encoding]::UTF8;$f.SelectedPath}`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", ps)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readClipboard() string {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "[Console]::OutputEncoding=[Text.Encoding]::UTF8;Get-Clipboard -Raw")
	hideWindow(cmd)
	out, _ := cmd.Output()
	return string(out)
}
