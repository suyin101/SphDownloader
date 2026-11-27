//go:build windows

package main

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// scanWeChatMemory 依次读微信进程（WeChatAppEx.exe，includeMain 时再加 Weixin.exe）的可读内存，每块交给 fn。
// first 里的进程（上次找到钥匙的）先读；每读完一个进程问一次 stop，返回 true 就不往下读了。
// 只读不写，不注入。返回成功打开的进程数。
func scanWeChatMemory(includeMain bool, first []uint32, fn func(pid uint32, buf []byte), stop func(pid uint32) bool) (int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snap)
	var pids []uint32
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		isMain := name == "weixin.exe" || name == "wechat.exe"
		if name == "wechatappex.exe" || (isMain && (includeMain || containsPid(first, pe.ProcessID))) {
			pids = append(pids, pe.ProcessID)
		}
	}
	pids = orderPids(pids, first)
	if len(pids) == 0 {
		return 0, errors.New("没找到微信进程")
	}
	buf := make([]byte, 64<<20)
	opened := 0
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
		if err != nil {
			continue
		}
		opened++
		scanProcess(h, buf, func(b []byte) { fn(pid, b) })
		windows.CloseHandle(h)
		if stop != nil && stop(pid) {
			break
		}
	}
	if opened == 0 {
		return 0, errors.New("打不开微信进程（试试右键“以管理员身份运行”下载器）")
	}
	return opened, nil
}

func scanProcess(h windows.Handle, buf []byte, fn func([]byte)) {
	const (
		memCommit = 0x1000
		memImage  = 0x1000000
		pageGuard = 0x100
		pageNoAcc = 0x01
		readable  = 0x02 | 0x04 | 0x08 | 0x20 | 0x40 | 0x80
	)
	var mbi windows.MemoryBasicInformation
	addr := uintptr(0x10000)
	for addr < 0x7FFFFFFF0000 {
		if err := windows.VirtualQueryEx(h, addr, &mbi, unsafe.Sizeof(mbi)); err != nil {
			return
		}
		size := mbi.RegionSize
		if size == 0 {
			return
		}
		base := mbi.BaseAddress
		ok := mbi.State == memCommit && mbi.Type != memImage &&
			mbi.Protect&(pageGuard|pageNoAcc) == 0 && mbi.Protect&readable != 0 && size <= uintptr(len(buf))
		if ok {
			var n uintptr
			windows.ReadProcessMemory(h, base, &buf[0], size, &n) // 部分页读不到时 n 是读到的长度
			if n > 0 {
				fn(buf[:n])
			}
		}
		next := base + size
		if next <= addr {
			return
		}
		addr = next
	}
}
