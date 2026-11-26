package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormatTranscript(t *testing.T) {
	out := `Started!
0.070 -- 4.492: 很多家长一张嘴，孩子就不想跟你说话，甚至你刚蹦出第一个字。
4.998 -- 12.684: 孩子就开始冲你发脾气
13.190 -- 15.052: 你再这么摆烂下去，将来怎么办？
30.000 -- 33.000: 首先 我们 来看 AI 的 情况。
num threads: 4`
	segs := parseSegments(strings.ReplaceAll(out, "\n", "\r\n"))
	if len(segs) != 4 || segs[1].Start != 4.998 {
		t.Fatalf("分段解析不对：%+v", segs)
	}
	got := formatTranscript(segs)
	want := "很多家长一张嘴，孩子就不想跟你说话，甚至你刚蹦出第一个字。孩子就开始冲你发脾气，你再这么摆烂下去，将来怎么办？\n\n首先我们来看AI的情况。"
	if got != want {
		t.Fatalf("排版不对：\n%s\n期望：\n%s", got, want)
	}
	if formatTranscript(nil) != "" {
		t.Fatal("没有内容应该是空")
	}
}

// 组件下载：断点续传、校验、解压、第二次不再下载、坏地址自动换下一个。
func TestASRDownload(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "rt.zip")
	makeZip(t, zipPath, map[string]string{exeName(): "#!/bin/sh\n", "silero_vad.onnx": "vad"})
	zipData, _ := os.ReadFile(zipPath)
	model := strings.Repeat("m", 300000)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		switch r.URL.Path {
		case "/bad/model":
			http.Error(w, "no", 404)
		case "/model":
			http.ServeContent(w, r, "model", time.Now(), strings.NewReader(model))
		case "/rt.zip":
			http.ServeContent(w, r, "rt.zip", time.Now(), strings.NewReader(string(zipData)))
		}
	}))
	defer srv.Close()
	old := asrParts
	defer func() { asrParts = old }()
	asrParts = func() []asrPart {
		return []asrPart{
			{Name: asrRuntimeName(), Size: int64(len(zipData)), SHA: sha(zipData), URLs: []string{srv.URL + "/rt.zip"}, Zip: true},
			{Name: "model.int8.onnx", Size: int64(len(model)), SHA: sha([]byte(model)), URLs: []string{srv.URL + "/bad/model", srv.URL + "/model"}},
		}
	}
	asrDir := filepath.Join(dir, "asr")
	os.MkdirAll(asrDir, 0o755)
	os.WriteFile(filepath.Join(asrDir, "model.int8.onnx.part"), []byte(model[:1000]), 0o644) // 上次下了一半
	tr := &Transcriber{dir: asrDir}
	if err := tr.ensure(); err != nil {
		t.Fatal(err)
	}
	if st := tr.State(); st.Status != "ready" || st.Progress != st.Total {
		t.Fatalf("状态不对：%+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(asrDir, "model.int8.onnx")); string(b) != model {
		t.Fatal("模型内容不对（续传拼错了？）")
	}
	if !fileExists(tr.exe()) || !fileExists(filepath.Join(asrDir, "silero_vad.onnx")) {
		t.Fatal("没解压出程序")
	}
	n := atomic.LoadInt32(&hits)
	if err := tr.ensure(); err != nil || atomic.LoadInt32(&hits) != n {
		t.Fatal("装好后不应该再下载")
	}
}

func exeName() string { return filepath.Base((&Transcriber{dir: "."}).exe()) }

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ASR_DIR=已装好组件的目录 SAMPLE_MP4=视频 go test -run TestTranscribeLive -v
func TestTranscribeLive(t *testing.T) {
	dir, mp4 := os.Getenv("ASR_DIR"), os.Getenv("SAMPLE_MP4")
	if dir == "" || mp4 == "" {
		t.Skip("没给 ASR_DIR / SAMPLE_MP4")
	}
	tmp := t.TempDir()
	video := filepath.Join(tmp, "测试 视频.mp4")
	b, _ := os.ReadFile(mp4)
	os.WriteFile(video, b, 0o644)
	tr := &Transcriber{dir: dir}
	if !tr.installed() {
		t.Fatal("组件没装好")
	}
	start := time.Now()
	txt, err := tr.run(video, FeedInfo{Title: "测试标题", Nickname: "作者", CreateTime: 1763400000})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := readTranscript(txt)
	t.Logf("用时 %v\n%s", time.Since(start).Round(time.Millisecond), out)
	if !strings.HasPrefix(out, "测试标题\n作者：作者") || len([]rune(out)) < 30 {
		t.Fatal("文案内容不对")
	}
}

func makeZip(t *testing.T, path string, files map[string]string) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
}
