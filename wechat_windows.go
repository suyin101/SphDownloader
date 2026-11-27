//go:build windows

package main

import (
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wechatMainPids 正在运行的微信主程序（Weixin.exe / WeChat.exe）。
func wechatMainPids() []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []uint32
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if name == "weixin.exe" || name == "wechat.exe" {
			out = append(out, pe.ProcessID)
		}
	}
	return out
}

func wechatRunning() bool { return len(wechatMainPids()) > 0 }

// wechatExePath 趁微信还开着记下它的路径，改完配置后用它重新打开。
func wechatExePath() string {
	for _, pid := range wechatMainPids() {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			continue
		}
		buf := make([]uint16, 1024)
		n := uint32(len(buf))
		err = windows.QueryFullProcessImageName(h, 0, &buf[0], &n)
		windows.CloseHandle(h)
		if err == nil && n > 0 {
			return windows.UTF16ToString(buf[:n])
		}
	}
	return ""
}

func openWeChat(exe string) {
	if exe == "" {
		logf("请手动打开微信，从左侧重新进入视频号")
		return
	}
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: 0x00000008} // DETACHED_PROCESS：别跟着下载器一起关
	if err := cmd.Start(); err != nil {
		logf("没能自动打开微信（%v），请手动打开", err)
	}
}
