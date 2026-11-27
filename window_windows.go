//go:build windows

package main

import (
	"os"
	"path/filepath"

	webview2 "github.com/jchv/go-webview2"
)

// runWindow 打开软件窗口（用系统自带的 WebView2），窗口关掉才返回。
func runWindow(url string) {
	// 窗口在后台（你在微信里看视频）时别被系统降速，列表才能实时更新
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disable-background-timer-throttling --disable-renderer-backgrounding --disable-backgrounding-occluded-windows")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(dataDir, "webview2"),
		WindowOptions: webview2.WindowOptions{
			Title: "视频号下载器", Width: 1080, Height: 760, Center: true, IconId: 1,
		},
	})
	if w == nil { // 极少数电脑没有 WebView2，就用浏览器打开
		openURL(url)
		select {}
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
}
