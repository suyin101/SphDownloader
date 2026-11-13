package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryNoRedownload(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.mp4")
	os.WriteFile(f, []byte("x"), 0o644)
	hp := filepath.Join(dir, "history.json")

	s := NewStore(dir)
	s.loadHistory(hp)
	s.mu.Lock()
	s.recordLocked(&Item{FeedInfo: FeedInfo{ID: "v1", Title: "标题"}, File: f, LinkKey: "sph:AAA"})
	s.mu.Unlock()

	// 重启软件后：同一视频再出现 → 直接显示已下载，不排队
	s2 := NewStore(dir)
	s2.AutoDownload = true
	s2.loadHistory(hp)
	s2.AddReport("current", []FeedInfo{{ID: "v1", URL: "http://x/?encfilekey=k&token=t"}}, "https://channels.weixin.qq.com/web/pages/home")
	if it, _ := s2.Get("v1"); it.Status != "done" || it.File != f {
		t.Fatalf("want done from history, got %s %s", it.Status, it.File)
	}
	// 再贴同一个链接 → 不再交给微信打开
	if n := s2.AddLinks("https://weixin.qq.com/sph/AAA"); n != 0 {
		t.Fatal("link already downloaded should not be added again")
	}
	if u := s2.NextTask(); u != "" {
		t.Fatal("no task expected")
	}
	// 手动“重新下载”仍然可以
	if n := s2.Enqueue([]string{"v1"}); n != 1 {
		t.Fatal("manual redownload should work")
	}
	// 文件被删了 → 视为没下载过
	os.Remove(f)
	s3 := NewStore(dir)
	s3.loadHistory(hp)
	s3.AddReport("list", []FeedInfo{{ID: "v1", URL: "http://x/?encfilekey=k&token=t"}}, "")
	if it, _ := s3.Get("v1"); it.Status != "seen" {
		t.Fatal(it.Status)
	}
}

// 链接任务对上一个以前下载过的视频：不重复下载
func TestLinkMatchAlreadyDownloaded(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.mp4")
	os.WriteFile(f, []byte("x"), 0o644)
	s := NewStore(dir)
	s.history["real"] = histEntry{File: f}
	s.items["link:sph:abc"] = &Item{Key: "link:sph:abc", LinkKey: "sph:abc", Source: "link", Status: "waiting", FeedInfo: FeedInfo{ID: "link:sph:abc"}}
	s.tasks = append(s.tasks, &linkTask{itemKey: "link:sph:abc", exportID: "export/E1", feedURL: "u", state: "pending"})
	s.NextTask()
	s.AddReport("current", []FeedInfo{{ID: "real", URL: "http://x/?encfilekey=k&token=t"}}, "https://channels.weixin.qq.com/web/pages/feed?eid=export%2FE1")
	if it, _ := s.Get("real"); it.Status != "done" {
		t.Fatal(it.Status)
	}
}

func TestClear(t *testing.T) {
	s := NewStore(t.TempDir())
	s.items["a"] = &Item{Key: "a", Status: "seen"}
	s.items["b"] = &Item{Key: "b", Status: "downloading"}
	s.items["link:x"] = &Item{Key: "link:x", Status: "waiting"}
	s.tasks = append(s.tasks, &linkTask{itemKey: "link:x", state: "pending"})
	if n := s.Clear(); n != 2 {
		t.Fatal(n)
	}
	if _, ok := s.items["b"]; !ok {
		t.Fatal("downloading item should stay")
	}
	if s.NextTask() != "" {
		t.Fatal("cleared link task should be cancelled")
	}
}

// 嗅探开关：关掉后端口释放、已有连接断开；能再打开
func TestSnifferToggle(t *testing.T) {
	setCalls, unsetCalls := 0, 0
	setSysProxy = func(string) (string, error) { setCalls++; return "", nil }
	unsetSysProxy = func() { unsetCalls++ }
	defer func() { setSysProxy, unsetSysProxy = enableSystemProxy, restoreSystemProxy }()

	old := proxyPort
	proxyPort = 18877
	defer func() { proxyPort = old }()
	ca, _, _ := loadOrCreateCA(t.TempDir())
	sn := &Sniffer{ca: ca, store: NewStore(t.TempDir())}
	if err := sn.Start(); err != nil {
		t.Skip("port busy:", err) // 用户的软件正开着
	}
	addr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	sn.Stop()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("existing connection should be closed")
	}
	if _, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		t.Fatal("port should be released")
	}
	if sn.On() {
		t.Fatal("should be off")
	}
	if err := sn.Start(); err != nil {
		t.Fatal("restart failed:", err)
	}
	sn.Stop()
	if setCalls != 2 || unsetCalls != 2 {
		t.Fatalf("set=%d unset=%d", setCalls, unsetCalls)
	}
}
