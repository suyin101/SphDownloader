package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 视频号页面上报一条新视频后，界面应该在半秒内收到“有变化”的通知（以前要等下一次每秒轮询，后台时更久）。
func TestEventsPushQuickly(t *testing.T) {
	dataDir = t.TempDir()
	s := NewStore(t.TempDir())
	cfg := config{}
	srv := httptest.NewServer(uiHandler(s, &cfg))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data:") {
				lines <- sc.Text()
			}
		}
	}()
	<-lines // 连上时的第一次
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	s.AddReport("current", []FeedInfo{{ID: "v1", Title: "新视频", URL: "https://finder.video.qq.com/x?encfilekey=a&token=b"}}, "")
	select {
	case <-lines:
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Fatalf("通知太慢：%v", d)
		} else {
			t.Logf("新视频 %v 后通知到界面", d.Round(time.Millisecond))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2 秒内没收到通知")
	}
}

// 页面钩子漏报时的兜底：预加载（一阵就完）不算，持续在读的才算；钩子在报时完全听钩子的。
func TestOnWebMedia(t *testing.T) {
	dataDir = t.TempDir()
	s := NewStore(t.TempDir())
	u := func(ek string) string {
		return "https://finder.video.qq.com/251/20302/stodownload?encfilekey=" + ek + "&token=x"
	}
	s.AddReport("list", []FeedInfo{{ID: "a", Title: "A", URL: u("EKA")}, {ID: "b", Title: "B", URL: u("EKB")}}, "")
	ago := func(ek string, d time.Duration) { s.mu.Lock(); s.webMedia[ek] = time.Now().Add(-d); s.mu.Unlock() }

	// 1. 正在看 A，播放器同时预加载 B（读到几 MB）：B 不能算正在看
	s.OnWebMedia("EKA", 1<<20)
	s.OnWebMedia("EKB", 1<<20)
	s.OnWebMedia("EKB", 3<<20)
	if s.CurrentKey() != "" {
		t.Fatalf("预加载的一阵读不该算正在看：%s", s.CurrentKey())
	}
	// 2. A 持续在读（离第一次 ≥3 秒）：算正在看
	ago("EKA", 5*time.Second)
	s.OnWebMedia("EKA", 4<<20)
	if s.CurrentKey() != "a" {
		t.Fatalf("持续在读的应该是正在看：%s", s.CurrentKey())
	}
	// 3. 刚改完 4 秒内，B 也在读：不改（防收尾请求抢第一行）
	ago("EKB", 10*time.Second)
	s.OnWebMedia("EKB", 5<<20)
	if s.CurrentKey() != "a" {
		t.Fatal("刚改过 4 秒内不该再改")
	}
	// 4. 过了 4 秒，B 持续在读（划到 B 了）：改成 B
	s.mu.Lock()
	s.webMarked = time.Now().Add(-5 * time.Second)
	s.mu.Unlock()
	s.OnWebMedia("EKB", 6<<20)
	if s.CurrentKey() != "b" {
		t.Fatalf("划到 B 持续在读，应该是 B：%s", s.CurrentKey())
	}
	// 5. 页面钩子报了正在看 A：之后 15 秒内播放器怎么读都听钩子的
	s.AddReport("current", []FeedInfo{{ID: "a", Title: "A", URL: u("EKA")}}, "")
	s.mu.Lock()
	s.webMarked = time.Time{}
	s.mu.Unlock()
	s.OnWebMedia("EKB", 7<<20)
	if s.CurrentKey() != "a" {
		t.Fatalf("钩子刚报过，应该听钩子的：%s", s.CurrentKey())
	}
	items, _ := s.Snapshot()
	if items[0].Key != "a" {
		t.Fatalf("第一行应该是钩子报的 A：%s", items[0].Key)
	}
	s.OnWebMedia("UNKNOWN", 1<<20)
	s.OnWebMedia("UNKNOWN", 2<<20)
}

// 下载器重启期间页面报的视频会丢：刚启动没收到过上报、或正在播的视频不在列表里时，让页面补报一遍。
func TestTakeResend(t *testing.T) {
	dataDir = t.TempDir()
	s := NewStore(t.TempDir())
	if !s.TakeResend() {
		t.Fatal("刚启动还没收到过上报，应该让页面补报")
	}
	s.AddReport("list", []FeedInfo{{ID: "a", Title: "A", URL: "https://finder.video.qq.com/251/20302/stodownload?encfilekey=EKA&token=x"}}, "")
	if s.TakeResend() {
		t.Fatal("已经收到上报，不用补报")
	}
	// 正在播一条列表里没有的（钩子 15 秒内没报过）
	s.OnWebMedia("EKX", 1<<20)
	s.mu.Lock()
	s.webMedia["EKX"] = time.Now().Add(-5 * time.Second)
	s.mu.Unlock()
	s.OnWebMedia("EKX", 2<<20)
	if !s.TakeResend() {
		t.Fatal("正在播的不在列表里，应该让页面补报")
	}
	if s.TakeResend() {
		t.Fatal("补报请求只发一次")
	}
	s.OnWebMedia("EKX", 3<<20)
	if s.TakeResend() {
		t.Fatal("同一条不在列表里的只请求一次补报")
	}
}

// 播放器开始播放带着地址：按 encfilekey 对上列表里已有的那条，立即排第一、标正在看。
func TestOnPlaying(t *testing.T) {
	dataDir = t.TempDir()
	s := NewStore(t.TempDir())
	u := func(ek string) string {
		return "https://finder.video.qq.com/251/20302/stodownload?encfilekey=" + ek + "&token=x"
	}
	s.AddReport("list", []FeedInfo{{ID: "a", Title: "A", URL: u("EKA")}, {ID: "b", Title: "B", URL: u("EKB")}}, "")
	s.TakeResend()
	if !s.OnPlaying("", u("EKB")+"&m=1") {
		t.Fatal("应该对上 B")
	}
	items, _ := s.Snapshot()
	if s.CurrentKey() != "b" || items[0].Key != "b" {
		t.Fatalf("B 应该是正在看、排第一：%s %s", s.CurrentKey(), items[0].Key)
	}
	if s.OnPlaying("", u("EKZ")) || !s.TakeResend() {
		t.Fatal("不在列表里的要让页面补报")
	}
	if s.OnPlaying("", "blob:https://channels.weixin.qq.com/x") {
		t.Fatal("blob 地址对不上")
	}
}
