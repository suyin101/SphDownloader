//go:build darwin

package main

import "os/exec"

func wechatRunning() bool {
	return exec.Command("pgrep", "-x", "WeChat").Run() == nil
}

func wechatExePath() string { return "" }

func openWeChat(string) {
	if err := exec.Command("open", "-b", "com.tencent.xinWeChat").Start(); err != nil {
		logf("没能自动打开微信（%v），请手动打开", err)
	}
}
