//go:build !windows

package main

import "errors"

// Mac 版微信的视频号还是网页，不需要读内存。
func scanWeChatMemory(includeMain bool, fn func(buf []byte)) (int, error) {
	return 0, errors.New("只有 Windows 版需要")
}
