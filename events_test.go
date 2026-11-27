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
