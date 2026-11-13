//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
void sphdlSetupMenu(void);
*/
import "C"

import (
	"runtime"

	webview "github.com/webview/webview_go"
)

func init() { runtime.LockOSThread() } // 窗口必须在主线程

//export sphdlWillTerminate
func sphdlWillTerminate() { restoreSystemProxy() }

// runWindow 打开软件窗口，窗口关掉才返回。
func runWindow(url string) {
	w := webview.New(false)
	if w == nil {
		openURL(url)
		select {}
	}
	defer w.Destroy()
	C.sphdlSetupMenu()
	w.SetTitle("视频号下载器")
	w.SetSize(1080, 760, webview.HintNone)
	w.Navigate(url)
	w.Run()
}
