//go:build windows

package main

import (
	"path/filepath"
	"runtime"
	"syscall"

	webview2 "github.com/jchv/go-webview2"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procShowWindow = user32.NewProc("ShowWindow")
	procSetWinPos  = user32.NewProc("SetWindowPos")
)

// browserFetchDouyin Windows：单独一个线程开一个隐藏的 WebView2 窗口（有自己的消息循环），拿到数据就关掉。
func browserFetchDouyin(id, script string) (string, error) {
	ch := make(chan string, 8)
	ready := make(chan webview2.WebView, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(done)
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			DataPath:      filepath.Join(dataDir, "webview2-douyin"), // 记住抖音给的游客身份
			WindowOptions: webview2.WindowOptions{Title: "抖音验证：完成后会自动继续", Width: 1100, Height: 760, IconId: 1},
		})
		if w == nil {
			ready <- nil
			return
		}
		hwnd := uintptr(w.Window())
		procShowWindow.Call(hwnd, 0) // SW_HIDE
		w.Bind("sphdlDouyin", func(s string) {
			select {
			case ch <- s:
			default:
			}
		})
		w.Init(script)
		w.Navigate("https://www.douyin.com/video/" + id)
		ready <- w
		w.Run()
		w.Destroy()
	}()
	w := <-ready
	if w == nil {
		return "", errNoWebView2
	}
	defer func() {
		w.Dispatch(func() { w.Terminate() })
		<-done
	}()
	return waitDouyin(ch, func() {
		w.Dispatch(func() {
			hwnd := uintptr(w.Window())
			procShowWindow.Call(hwnd, 5)                           // SW_SHOW
			procSetWinPos.Call(hwnd, ^uintptr(0), 0, 0, 0, 0, 0x3) // 置顶一下（HWND_TOPMOST，不改大小位置）
			procSetWinPos.Call(hwnd, ^uintptr(1), 0, 0, 0, 0, 0x3) // 再取消置顶
		})
	})
}
