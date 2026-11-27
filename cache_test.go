package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 进度估算：一直往前走，不到 100，超时很久也停在 98 以下。
func TestEstPct(t *testing.T) {
	last := 0
	for e := 0.0; e < 300; e += 0.5 {
		p := estPct(e, 30)
		if p < last || p > 98 || p < 8 {
			t.Fatalf("elapsed %.1f pct %d last %d", e, p, last)
		}
		last = p
	}
	if p := estPct(15, 30); p < 50 || p > 56 {
		t.Fatalf("half way pct %d", p)
	}
}

// 速度记下来以后，预计用时按记下的算。
func TestSpeedMemo(t *testing.T) {
	tr := &Transcriber{dir: t.TempDir()}
	def := tr.expectSecs(asrFast, 100)
	tr.saveSpeed(asrFast, 100, 53) // 每秒 2 秒声音（扣掉 3 秒加载）
	got := tr.expectSecs(asrFast, 100)
	if got <= def || got < 20 {
		t.Fatalf("default %.1f after %.1f", def, got)
	}
}

// 清除缓存：只删日志/临时文件和组件，设置、证书、下载记录、视频都不动。
func TestClearCache(t *testing.T) {
	old := dataDir
	dataDir = t.TempDir()
	defer func() { dataDir = old }()
	w := func(p string, n int) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, n), 0o644)
	}
	w(filepath.Join(dataDir, "debug", "trace.log"), 1000)
	w(filepath.Join(dataDir, "debug", "files", "a.js"), 500)
	w(filepath.Join(dataDir, "asr", "work", "job.wav"), 300)
	w(filepath.Join(dataDir, "asr", "model.int8.onnx"), 2000)
	keep := []string{"config.json", "ca.crt", "ca.key", "history.json"}
	for _, k := range keep {
		w(filepath.Join(dataDir, k), 10)
	}
	vids := t.TempDir()
	w(filepath.Join(vids, "a.mp4"), 10)
	s := NewStore(vids)
	s.tx = &Transcriber{dir: filepath.Join(dataDir, "asr"), queue: make(chan *Item, 1), ready: map[string]bool{asrFast: true}}

	sz := cacheSizes(s)
	if sz["logs"] != 1800 || sz["asr"] != 2000 {
		t.Fatalf("sizes %v", sz)
	}
	if _, err := clearCache(s, true, false); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dataDir, "debug", "files", "a.js")) || fileExists(filepath.Join(dataDir, "asr", "work", "job.wav")) {
		t.Fatal("logs not cleared")
	}
	if !fileExists(filepath.Join(dataDir, "asr", "model.int8.onnx")) {
		t.Fatal("asr removed without asking")
	}
	s.tx.busy.Store(true)
	if _, err := clearCache(s, false, true); err == nil {
		t.Fatal("should refuse while transcribing")
	}
	s.tx.busy.Store(false)
	if _, err := clearCache(s, false, true); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dataDir, "asr", "model.int8.onnx")) || s.tx.ready[asrFast] {
		t.Fatal("asr not cleared")
	}
	for _, k := range keep {
		if !fileExists(filepath.Join(dataDir, k)) {
			t.Fatal("deleted", k)
		}
	}
	if !fileExists(filepath.Join(vids, "a.mp4")) {
		t.Fatal("deleted video")
	}
}
