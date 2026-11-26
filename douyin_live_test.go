//go:build darwin

package main

import (
	"os"
	"testing"
)

// Mac 上真实打开抖音：DY_LIVE="分享文字" go test -run TestDouyinLive -v
// WKWebView 要在主线程跑事件循环，所以测试放到别的线程，主线程跑循环。
func TestMain(m *testing.M) {
	if os.Getenv("DY_LIVE") == "" {
		os.Exit(m.Run())
	}
	go func() { os.Exit(m.Run()) }()
	runCocoaLoopForTest()
}
