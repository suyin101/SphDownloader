package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeMP4 造一个结构合法的 mp4：ftyp + moov + mdat，正好 total 字节。
func fakeMP4(total int) []byte {
	b := make([]byte, total)
	rand.New(rand.NewSource(1)).Read(b)
	binary.BigEndian.PutUint32(b[0:], 24)
	copy(b[4:], "ftypisom")
	binary.BigEndian.PutUint32(b[24:], 3000)
	copy(b[28:], "moov")
	binary.BigEndian.PutUint32(b[3024:], uint32(total-3024))
	copy(b[3028:], "mdat")
	return b
}

func encrypt(plain []byte, seed uint64) []byte {
	c := append([]byte(nil), plain...)
	ks := keystream(seed, encryptedLen)
	for i := range ks {
		c[i] ^= ks[i]
	}
	return c
}

func serveVideo(t *testing.T, data []byte) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("X-enclen", "131072")
		http.ServeContent(w, r, "v.mp4", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const testEK = "Cvvj5Ix3eewK0tHtibORqcsqchXNh0Gf3sJcaYqC2rQ"

func memWith(parts ...[]byte) []byte {
	noise := make([]byte, 300000)
	rand.New(rand.NewSource(2)).Read(noise)
	var b []byte
	for _, p := range parts {
		b = append(b, noise[:100001]...) // 奇数长度，测 UTF-16 对齐
		b = append(b, p...)
	}
	return append(b, noise...)
}

func TestExtractSeedUTF16(t *testing.T) {
	const seed = 979861605
	plain := fakeMP4(400000)
	c := encrypt(plain, seed)
	js := `{"objectDesc":{"description":"测试标题 #话题","media":[{"url":"https://finder.video.qq.com/251/20302/stodownload?encfilekey=` + testEK + `&token=x","decodeKey":"979861605"}]},"contact":{"nickname":"作者甲"}}`
	mem := memWith(u16(js), []byte("decodeKey: 12345678 other"))
	ss := seedSet{}
	extractSeeds(mem, []string{ekPrefix(testEK)}, ss)
	got, info, ok := ss.match(c[:16], ekPrefix(testEK))
	if !ok || got != seed {
		t.Fatalf("没找到钥匙 ok=%v got=%d 候选=%d", ok, got, len(ss))
	}
	if info.title != "测试标题 #话题" || info.nick != "作者甲" {
		t.Fatalf("标题不对：%+v", info)
	}
	if _, _, ok := ss.match(encrypt(plain, 5)[:16], ""); ok {
		t.Fatal("不该匹配")
	}
}

func TestExtractSeedReportAndNearby(t *testing.T) {
	ss := seedSet{}
	mem := memWith([]byte("15050086854154853712,https://finder.video.qq.com/251/20302/stodownload?encfilekey=abc&m=1,4242424242,14,0"),
		u16("encfilekey="+testEK+"&token=1 ... seed 777777777 ..."))
	extractSeeds(mem, []string{ekPrefix(testEK)}, ss)
	for _, want := range []uint64{4242424242, 777777777} {
		if ss[want] == nil {
			t.Fatalf("缺候选 %d", want)
		}
	}
}

func liteTestStore(t *testing.T, mem []byte) *Store {
	t.Helper()
	oldE, oldM, oldG := liteEnabled, memScan, liteScanGap
	liteEnabled, liteScanGap = true, 10*time.Millisecond
	memScan = func(includeMain bool, fn func([]byte)) (int, error) { fn(mem); return 1, nil }
	t.Cleanup(func() { liteEnabled, memScan, liteScanGap = oldE, oldM, oldG })
	dataDir = t.TempDir()
	return NewStore(t.TempDir())
}

func waitItem(t *testing.T, s *Store, cond func(Item) bool) Item {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		items, _ := s.Snapshot()
		for _, it := range items {
			if cond(it) {
				return it
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	items, _ := s.Snapshot()
	t.Fatalf("等不到：%+v", items)
	return Item{}
}

func playLite(s *Store, u string, n int) {
	h := http.Header{}
	h.Set("Content-Range", "bytes 0-1000/400000")
	h.Set("X-enclen", "131072")
	for i := 0; i < n; i++ {
		s.OnLiteMedia(u, "video/mp4", h, 206)
	}
}

func checkFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("下载的文件不对 err=%v len=%d", err, len(got))
	}
}

func TestLiteSeedDownload(t *testing.T) {
	const seed = 31415926535
	plain := fakeMP4(400000)
	srv := serveVideo(t, encrypt(plain, seed))
	js := `{"description":"猫猫跳舞","url":"` + srv.URL + `/251/20302/stodownload?encfilekey=` + testEK + `","decodeKey":"31415926535"}`
	s := liteTestStore(t, memWith(u16(js)))
	s.AutoDownload = true
	u := srv.URL + "/251/20302/stodownload?encfilekey=" + testEK + "&token=t&m=0123456789abcdef0123456789abcdef"
	playLite(s, u, 1)
	if s.PageState() != "lite" {
		t.Fatalf("状态 %s", s.PageState())
	}
	it := waitItem(t, s, func(it Item) bool { return it.Status == "done" })
	if it.Title != "猫猫跳舞" || it.Source != "lite" || it.Key != "lite:31415926535" {
		t.Fatalf("条目不对：%+v", it)
	}
	checkFile(t, it.File, plain)
	// 再播一次：不重复建条目
	playLite(s, u, 3)
	time.Sleep(200 * time.Millisecond)
	if items, _ := s.Snapshot(); len(items) != 1 {
		t.Fatalf("重复了：%d", len(items))
	}
	// 预览（边下边解密，含 Range）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Range", "bytes=0-99")
	os.Remove(it.File)
	s.ServePreview(rec, req, it.Key)
	if !bytes.Equal(rec.Body.Bytes(), plain[:100]) {
		t.Fatal("预览解密不对")
	}
}

func TestLiteHeaderFallback(t *testing.T) {
	plain := fakeMP4(400000)
	srv := serveVideo(t, encrypt(plain, 999)) // 内存里没有钥匙，只有解密好的开头
	other := fakeMP4(500000)                  // 别的视频的开头：结构对不上大小
	s := liteTestStore(t, memWith(other[:encryptedLen], plain[:encryptedLen]))
	u := srv.URL + "/251/20302/stodownload?encfilekey=" + testEK + "&token=t"
	playLite(s, u, 3) // 请求 3 次 = 正在看
	it := waitItem(t, s, func(it Item) bool { return it.Source == "lite" })
	if !strings.HasPrefix(it.Key, "lite:h") || s.CurrentKey() != it.Key {
		t.Fatalf("条目不对：%+v", it)
	}
	s.Enqueue([]string{it.Key})
	it = waitItem(t, s, func(it Item) bool { return it.Status == "done" })
	checkFile(t, it.File, plain)
	if filepath.Ext(it.File) != ".mp4" {
		t.Fatal(it.File)
	}
}

func TestLiteIgnoredWhenPageOnline(t *testing.T) {
	s := liteTestStore(t, nil)
	s.MarkPageSeen()
	playLite(s, "http://x/stodownload?encfilekey="+testEK, 3)
	if s.lite != nil {
		t.Fatal("网页模式下不该进自带播放器模式")
	}
}

// 代理转发时截到开头：不用再去服务器读，也能对上钥匙。
func TestLiteHeadFromProxy(t *testing.T) {
	const seed = 123456789
	c := encrypt(fakeMP4(400000), seed)
	s := liteTestStore(t, memWith([]byte(`"decodeKey":"123456789"`)))
	u := "http://127.0.0.1:1/251/20302/stodownload?encfilekey=" + testEK // 连不上，逼它只能用截到的开头
	h := http.Header{}
	h.Set("Content-Range", "bytes 0-999/400000")
	ek, want := s.OnLiteMedia(u, "video/mp4", h, 206)
	if !want || ek != testEK || !startsAtZero(206, h) {
		t.Fatalf("want=%v ek=%s", want, ek)
	}
	tap := &headTap{r: io.NopCloser(bytes.NewReader(c[:1000])), fn: func(b []byte) { s.SetLiteHead(ek, b) }}
	got, _ := io.ReadAll(tap)
	if !bytes.Equal(got, c[:1000]) {
		t.Fatal("转发内容被改了")
	}
	it := waitItem(t, s, func(it Item) bool { return it.Key == "lite:123456789" })
	if it.Status != "seen" {
		t.Fatal(it.Status)
	}
	if _, want := s.OnLiteMedia(u, "video/mp4", h, 206); want {
		t.Fatal("已经有开头了")
	}
}
