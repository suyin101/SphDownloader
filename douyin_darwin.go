//go:build darwin

package main

/*
#cgo LDFLAGS: -framework WebKit
#include <stdlib.h>
void sphdlDouyinOpen(const char *url, const char *script);
void sphdlDouyinShow(void);
void sphdlDouyinClose(void);
void sphdlRunLoopForTest(void);
*/
import "C"

import (
	"sync"
	"unsafe"
)

var (
	dyMsgMu sync.Mutex
	dyMsgCh chan string
)

//export sphdlDouyinMsg
func sphdlDouyinMsg(s *C.char) {
	msg := C.GoString(s)
	dyMsgMu.Lock()
	ch := dyMsgCh
	dyMsgMu.Unlock()
	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

func browserFetchDouyin(id, script string) (string, error) {
	ch := make(chan string, 8)
	dyMsgMu.Lock()
	dyMsgCh = ch
	dyMsgMu.Unlock()
	defer func() {
		dyMsgMu.Lock()
		dyMsgCh = nil
		dyMsgMu.Unlock()
		C.sphdlDouyinClose()
	}()
	cu, cs := C.CString("https://www.douyin.com/video/"+id), C.CString(script)
	C.sphdlDouyinOpen(cu, cs)
	C.free(unsafe.Pointer(cu))
	C.free(unsafe.Pointer(cs))
	return waitDouyin(ch, func() { C.sphdlDouyinShow() })
}

// runCocoaLoopForTest 测试用（douyin_live_test.go）：主线程跑事件循环，不会返回。
func runCocoaLoopForTest() { C.sphdlRunLoopForTest() }
