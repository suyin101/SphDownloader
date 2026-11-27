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
	asrParts = func(string) []asrPart {
		return []asrPart{
			{Name: asrRuntimeName(), Size: int64(len(zipData)), SHA: sha(zipData), URLs: []string{srv.URL + "/rt.zip"}, Zip: true},
			{Name: "model.int8.onnx", Size: int64(len(model)), SHA: sha([]byte(model)), URLs: []string{srv.URL + "/bad/model", srv.URL + "/model"}},
		}
	}
	asrDir := filepath.Join(dir, "asr")
	os.MkdirAll(asrDir, 0o755)
	os.WriteFile(filepath.Join(asrDir, "model.int8.onnx.part"), []byte(model[:1000]), 0o644) // 上次下了一半
	tr := &Transcriber{dir: asrDir, ready: map[string]bool{}}
	if err := tr.ensure(asrFast); err != nil {
		t.Fatal(err)
	}
	tr.SetMode(asrFast)
	if st := tr.State(); st.Status != "ready" {
		t.Fatalf("状态不对：%+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(asrDir, "model.int8.onnx")); string(b) != model {
		t.Fatal("模型内容不对（续传拼错了？）")
	}
	if !fileExists(tr.exe()) || !fileExists(filepath.Join(asrDir, "silero_vad.onnx")) {
		t.Fatal("没解压出程序")
	}
	n := atomic.LoadInt32(&hits)
	if err := tr.ensure(asrFast); err != nil || atomic.LoadInt32(&hits) != n {
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
	tr := &Transcriber{dir: dir, ready: map[string]bool{}}
	tr.SetMode(os.Getenv("ASR_MODE"))
	if !tr.installed(tr.Mode()) {
		t.Fatal("组件没装好")
	}
	start := time.Now()
	var pcts []int
	txt, err := tr.run(video, FeedInfo{Title: "测试标题", Nickname: "作者", CreateTime: 1763400000}, func(p int) { pcts = append(pcts, p) })
	if err != nil {
		t.Fatal(err)
	}
	out, _ := readTranscript(txt)
	t.Logf("用时 %v\n%s", time.Since(start).Round(time.Millisecond), out)
	t.Logf("进度：%v", pcts)
	if len(pcts) < 3 || pcts[len(pcts)-1] < 90 {
		t.Fatalf("进度没有一路报到接近 100：%v", pcts)
	}
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

// 真实从镜像下载某个模式的组件：ASR_DIR=目录 ASR_MODE=accurate go test -count=1 -run TestASRRealDownload -v
func TestASRRealDownload(t *testing.T) {
	dir := os.Getenv("ASR_DIR")
	if dir == "" || os.Getenv("ASR_REAL") == "" {
		t.Skip()
	}
	tr := &Transcriber{dir: dir, ready: map[string]bool{}}
	mode := normMode(os.Getenv("ASR_MODE"))
	start := time.Now()
	if err := tr.ensure(mode); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s 组件就绪，用时 %v", mode, time.Since(start).Round(time.Second))
}

// 完整安装包自带快速模式组件：启动时直接从安装包装好、不联网；选精准但还没下好时先用快速，精准在后台下。
func TestASRBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "asr-bundle")
	os.MkdirAll(bundle, 0o755)
	makeZip(t, filepath.Join(bundle, asrRuntimeName()), map[string]string{exeName(): "#!/bin/sh\n", "silero_vad.onnx": "vad"})
	zipData, _ := os.ReadFile(filepath.Join(bundle, asrRuntimeName()))
	model, tokens := strings.Repeat("m", 300000), "tok"
	os.WriteFile(filepath.Join(bundle, "model.int8.onnx"), []byte(model), 0o644)
	os.WriteFile(filepath.Join(bundle, "tokens.txt"), []byte(tokens), 0o644)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "no", 404) // 网络不通也要能装好快速模式
	}))
	defer srv.Close()
	oldParts, oldBundle := asrParts, asrBundleDir
	defer func() { asrParts, asrBundleDir = oldParts, oldBundle }()
	asrBundleDir = func() string { return bundle }
	asrParts = func(mode string) []asrPart {
		if mode == asrFast {
			return []asrPart{
				{Name: asrRuntimeName(), Size: int64(len(zipData)), SHA: sha(zipData), URLs: []string{srv.URL + "/rt.zip"}, Zip: true},
				{Name: "model.int8.onnx", Size: int64(len(model)), SHA: sha([]byte(model)), URLs: []string{srv.URL + "/model"}},
				{Name: "tokens.txt", Size: int64(len(tokens)), SHA: sha([]byte(tokens)), URLs: []string{srv.URL + "/tokens"}},
			}
		}
		return []asrPart{{Name: "qwen3/decoder.int8.onnx", Size: 5, SHA: "x", URLs: []string{srv.URL + "/qwen"}}}
	}
	dataDir = dir
	tr := NewTranscriber(filepath.Join(dir, "asr"), NewStore(t.TempDir()))
	deadline := time.Now().Add(5 * time.Second)
	for !tr.installed(asrFast) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !tr.installed(asrFast) {
		t.Fatal("启动后应该从安装包装好快速模式")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("从安装包装不该联网，实际请求了 %d 次", n)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "asr", "model.int8.onnx")); string(b) != model {
		t.Fatal("模型没拷对")
	}
	// 默认精准、还没下好：这次用快速，精准在后台下（这里下载会失败，也不影响快速模式）
	tr.SetMode(asrAccurate)
	if m := tr.pickMode(); m != asrFast {
		t.Fatalf("精准没下好时应该先用快速，实际 %s", m)
	}
	if err := tr.ensure(asrFast); err != nil {
		t.Fatalf("后台在下精准时快速模式不该被卡住：%v", err)
	}
	for tr.bgAcc.Load() && time.Now().Before(deadline.Add(10*time.Second)) {
		time.Sleep(20 * time.Millisecond)
	}
	// 选的是快速：就用快速
	tr.SetMode(asrFast)
	if m := tr.pickMode(); m != asrFast {
		t.Fatal("选快速就用快速")
	}
}
