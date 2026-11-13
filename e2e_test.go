package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 完整流程：页面上报（经过代理 + 本机证书）→ 下载 → 解密 → 得到原始 MP4
func TestEndToEnd(t *testing.T) {
	sample := os.Getenv("SAMPLE_MP4")
	if sample == "" {
		t.Skip("set SAMPLE_MP4")
	}
	orig, _ := os.ReadFile(sample)
	const seed = uint64(2398474712)
	enc := append([]byte(nil), orig...)
	refDecryptData(enc, encryptedLen, seed) // 异或加密和解密是同一个操作

	video := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("encfilekey") != "K1" || r.URL.Query().Get("token") != "T1" || r.URL.Query().Get("X-snsvideoflag") != "" {
			http.Error(w, "bad", 403)
			return
		}
		w.Write(enc)
	}))
	defer video.Close()

	tmp := t.TempDir()
	ca, _, err := loadOrCreateCA(tmp)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(tmp, "out"))
	store.AutoDownload = true
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go http.Serve(ln, newProxy(ca, store, ""))

	pool := x509.NewCertPool()
	pool.AddCert(ca.Leaf)
	pu, _ := url.Parse("http://" + ln.Addr().String())
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: pool}}}

	body := fmt.Sprintf(`{"kind":"current","items":[{"id":"F1","title":"端到端 测试","nickname":"测试号","url":"%s/251/20302/stodownload?encfilekey=K1&token=T1&X-snsvideoflag=xWT111","decodeKey":"%d","createtime":1700000000}]}`, video.URL, seed)
	resp, err := c.Post("https://channels.weixin.qq.com/__sphdl/report", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}

	var it Item
	for i := 0; i < 100; i++ {
		items, _ := store.Snapshot()
		if len(items) == 1 && (items[0].Status == "done" || items[0].Status == "error") {
			it = items[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if it.Status != "done" {
		t.Fatalf("status=%s err=%s", it.Status, it.Err)
	}
	got, _ := os.ReadFile(it.File)
	if !bytes.Equal(got, orig) {
		t.Fatal("decrypted file differs from original")
	}
	t.Log("saved:", filepath.Base(it.File))

	// 同一条视频再上报一次，不会重复下载
	c.Post("https://channels.weixin.qq.com/__sphdl/report", "application/json", strings.NewReader(body))
	time.Sleep(200 * time.Millisecond)
	files, _ := os.ReadDir(filepath.Join(tmp, "out"))
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
}

// 解密钥匙错了要报错，而不是存一个坏文件
func TestWrongKeyRejected(t *testing.T) {
	sample := os.Getenv("SAMPLE_MP4")
	if sample == "" {
		t.Skip()
	}
	orig, _ := os.ReadFile(sample)
	enc := append([]byte(nil), orig...)
	refDecryptData(enc, encryptedLen, 111)
	video := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(enc) }))
	defer video.Close()
	store := NewStore(t.TempDir())
	store.AutoDownload = true
	store.AddReport("current", []FeedInfo{{ID: "x", URL: video.URL + "/?encfilekey=a&token=b", DecodeKey: "222"}}, "")
	for i := 0; i < 100; i++ {
		items, _ := store.Snapshot()
		if items[0].Status == "error" {
			return
		}
		if items[0].Status == "done" {
			t.Fatal("wrong key should fail")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout")
}
