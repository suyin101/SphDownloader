package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseLinks(t *testing.T) {
	text := `看看这个 https://weixin.qq.com/sph/AuQGcNgaw 还有
https://weixin.qq.com/sph/AuQGcNgaw（重复）
https://channels.weixin.qq.com/finder-preview/pages/sph?id=Bxyz_12-a&from=x
https://channels.weixin.qq.com/web/pages/feed?eid=export%2FUzFfAgtgek&context_id=1`
	ls := parseLinks(text)
	got := []string{}
	for _, l := range ls {
		got = append(got, l.key())
	}
	want := "sph:AuQGcNgaw,sph:Bxyz_12-a,eid:export/UzFfAgtgek"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v", got)
	}
}

// 链接任务：页面打开 feed 页后上报 current，要对上任务、替换占位并强制下载（即使关了自动下载）
func TestLinkTaskMatch(t *testing.T) {
	s := NewStore(t.TempDir())
	s.AutoDownload = false
	s.items["link:sph:abc"] = &Item{Key: "link:sph:abc", Source: "link", Status: "waiting", FeedInfo: FeedInfo{ID: "link:sph:abc", Title: "预览标题"}}
	s.tasks = append(s.tasks, &linkTask{itemKey: "link:sph:abc", exportID: "export/E1", feedURL: "https://channels.weixin.qq.com/web/pages/feed?eid=export%2FE1", state: "pending"})
	if u := s.NextTask(); !strings.Contains(u, "E1") {
		t.Fatal("task not handed out", u)
	}
	if u := s.NextTask(); u != "" {
		t.Fatal("only one task at a time")
	}
	// 别的页面的上报不能被误认
	s.AddReport("current", []FeedInfo{{ID: "other", URL: "http://x/?encfilekey=o&token=t"}}, "https://channels.weixin.qq.com/web/pages/home")
	s.AddReport("current", []FeedInfo{{ID: "real", URL: "http://127.0.0.1:1/?encfilekey=k&token=t", DecodeKey: "1"}},
		"https://channels.weixin.qq.com/web/pages/feed?eid=export%2FE1&context_id=1")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items["link:sph:abc"]; ok {
		t.Fatal("placeholder should be replaced")
	}
	it := s.items["real"]
	if it == nil || it.Source != "link" || it.Title != "预览标题" || (it.Status != "queued" && it.Status != "downloading" && it.Status != "error") {
		t.Fatalf("bad item %+v", it)
	}
	if s.items["other"].Status != "seen" {
		t.Fatal("auto download is off; other should not download")
	}
}

// 预览：带 Range 拖动进度条时，也要从正确位置解密
func TestPreviewRange(t *testing.T) {
	sample := os.Getenv("SAMPLE_MP4")
	if sample == "" {
		t.Skip()
	}
	orig, _ := os.ReadFile(sample)
	enc := append([]byte(nil), orig...)
	refDecryptData(enc, encryptedLen, 77)
	video := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "v.mp4", time.Now(), bytes.NewReader(enc))
	}))
	defer video.Close()
	s := NewStore(t.TempDir())
	s.AutoDownload = false
	s.AddReport("list", []FeedInfo{{ID: "p", URL: video.URL + "/?encfilekey=a&token=b", DecodeKey: "77"}}, "")
	for _, rg := range []string{"", "bytes=0-99", "bytes=1000-140000", "bytes=200000-"} {
		req := httptest.NewRequest("GET", "/api/preview?key=p", nil)
		if rg != "" {
			req.Header.Set("Range", rg)
		}
		rec := httptest.NewRecorder()
		s.ServePreview(rec, req, "p")
		body, _ := io.ReadAll(rec.Body)
		var start int
		if rg != "" {
			var a, b int
			if n, _ := fmtSscanf(rg, &a, &b); n >= 1 {
				start = a
			}
		}
		if !bytes.Equal(body, orig[start:start+len(body)]) || len(body) == 0 {
			t.Fatalf("range %q wrong (status %d, len %d)", rg, rec.Code, len(body))
		}
	}
}

func fmtSscanf(rg string, a, b *int) (int, error) {
	rg = strings.TrimPrefix(rg, "bytes=")
	x, y, _ := strings.Cut(rg, "-")
	n := 0
	if v, err := atoi(x); err == nil {
		*a = v
		n++
	}
	if v, err := atoi(y); err == nil {
		*b = v
		n++
	}
	return n, nil
}

func atoi(s string) (int, error) {
	v := 0
	if s == "" {
		return 0, io.EOF
	}
	for _, c := range s {
		v = v*10 + int(c-'0')
	}
	return v, nil
}

// 真实接口（需要联网）：LIVE=1 时跑
func TestResolveLinkLive(t *testing.T) {
	if os.Getenv("LIVE") != "1" {
		t.Skip()
	}
	info, err := resolveLink(linkRef{ShortURI: "AuQGcNgaw"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Nickname == "" || !strings.Contains(info.FeedURL, "eid=export%2F") {
		t.Fatalf("%+v", info)
	}
	t.Logf("%s | %s | %s", info.Nickname, info.Title[:30], info.FeedURL[:90])
}

// 默认不自动下载：刚打开视频号时自动播放的第一条只进列表
func TestDefaultNoAutoDownload(t *testing.T) {
	s := NewStore(t.TempDir())
	s.AddReport("current", []FeedInfo{{ID: "a", URL: "http://x/?encfilekey=k&token=t"}}, "https://channels.weixin.qq.com/web/pages/home")
	if it, _ := s.Get("a"); it.Status != "seen" {
		t.Fatal(it.Status)
	}
}

// 排序：打开过的永远在最上面（最近打开的第一），预加载的列表视频顺序不再跳动
func TestOrderOpenedFirst(t *testing.T) {
	s := NewStore(t.TempDir())
	feed := func(id string) FeedInfo { return FeedInfo{ID: id, URL: "http://x/?encfilekey=" + id + "&token=t"} }
	home := "https://channels.weixin.qq.com/web/pages/home"
	s.AddReport("current", []FeedInfo{feed("A")}, home)
	time.Sleep(2 * time.Millisecond)
	s.AddReport("list", []FeedInfo{feed("p1"), feed("p2")}, home) // 后台预加载
	time.Sleep(2 * time.Millisecond)
	s.AddReport("current", []FeedInfo{feed("B")}, home) // 打开下一个
	time.Sleep(2 * time.Millisecond)
	s.AddReport("list", []FeedInfo{feed("p3")}, home)
	s.AddReport("list", []FeedInfo{feed("p1")}, home) // 预加载重复出现，不能跳到前面
	keys := func() string {
		items, _ := s.Snapshot()
		out := ""
		for _, it := range items {
			out += it.Key + " "
		}
		return out
	}
	if got := keys(); got[:4] != "B A " {
		t.Fatalf("opened videos should be first: %s", got)
	}
	first := keys()
	for i := 0; i < 5; i++ {
		s.AddReport("list", []FeedInfo{feed("p2"), feed("p3")}, home)
		if keys() != first {
			t.Fatalf("order changed: %s -> %s", first, keys())
		}
	}
	if s.CurrentKey() != "B" {
		t.Fatal(s.CurrentKey())
	}
	// 回去再看 A → A 到第一
	s.AddReport("current", []FeedInfo{feed("A")}, home)
	if got := keys(); got[:4] != "A B " {
		t.Fatal(got)
	}
	// 粘贴多条链接：按粘贴顺序排在最上面
	s.AddLinks("https://weixin.qq.com/sph/L1\nhttps://weixin.qq.com/sph/L2")
	if got := keys(); got[:30] != "link:sph:L1 link:sph:L2 A B p3" {
		t.Fatal(got)
	}
}
