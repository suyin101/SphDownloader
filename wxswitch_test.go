package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPatchEngineConfigs(t *testing.T) {
	root := t.TempDir()
	old := radiumRootFn
	radiumRootFn = func() string { return root }
	defer func() { radiumRootFn = old }()
	mk := func(acct, ver, body string) string {
		p := filepath.Join(root, "users", acct, "xworker", "liteapp", "local", "packages", "ilinkres_uzhblidb", "engineConfig", ver, "dist", "engineConfig.json")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	on := "{\n  \"featureSwitch\": {\n    \"handleFinder\": true,\n    \"handleSearch\" : true,\n    \"other\": 1\n  },\n  \"x\": \"handleFinder\"\n}"
	a := mk("wxid_a", "174", on)
	b := mk("wxid_b", "175", `{"featureSwitch":{"handleFinder":false}}`)
	files, err := findEngineConfigs()
	if err != nil || len(files) != 2 {
		t.Fatalf("没找全：%v %v", files, err)
	}
	if st := (&liteSwitcher{asked: true}).State(); !st.On || st.Files != 2 {
		t.Fatalf("应该检测到开着：%+v", st)
	}
	n, err := patchEngineConfigs()
	if err != nil || n != 1 {
		t.Fatalf("改了 %d 个，%v", n, err)
	}
	got, _ := os.ReadFile(a)
	want := strings.Replace(on, `"handleFinder": true`, `"handleFinder": false`, 1)
	if string(got) != want {
		t.Fatalf("改错了：\n%s", got)
	}
	var v map[string]any
	if json.Unmarshal(got, &v) != nil {
		t.Fatal("改完不是合法 JSON")
	}
	if bak, _ := os.ReadFile(a + ".sphdl-bak"); string(bak) != on {
		t.Fatal("没备份原文件")
	}
	if _, err := os.Stat(b + ".sphdl-bak"); err == nil {
		t.Fatal("没改的文件不该备份")
	}
	if n, _ := patchEngineConfigs(); n != 0 {
		t.Fatal("已经关了的不该再改")
	}
	if st := (&liteSwitcher{asked: true}).State(); st.On {
		t.Fatalf("改完应该是关的：%+v", st)
	}
	// 微信升级后又打开：再改一次，备份保留最早的原文件
	os.WriteFile(a, []byte(on), 0o644)
	if n, _ := patchEngineConfigs(); n != 1 {
		t.Fatal("被改回去后应该能再改")
	}
}

func TestFindEngineConfigsFallbackWalk(t *testing.T) {
	root := t.TempDir()
	old := radiumRootFn
	radiumRootFn = func() string { return root }
	defer func() { radiumRootFn = old }()
	p := filepath.Join(root, "users", "u", "other", "place", "engineConfig.json") // 结构变了也能搜到
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"featureSwitch":{"handleFinder":true}}`), 0o644)
	files, _ := findEngineConfigs()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("%v", files)
	}
	radiumRootFn = func() string { return filepath.Join(root, "nope") }
	if files, err := findEngineConfigs(); len(files) != 0 || err != nil {
		t.Fatal("没有目录时应该安静地返回空")
	}
}

// Windows 默认网页模式：微信开着不动文件；微信一退出就自动改回网页模式（微信升级改回去也一样）。
func TestLiteWatch(t *testing.T) {
	root := t.TempDir()
	oldRoot, oldRun, oldEvery, oldSettle := radiumRootFn, wechatRunningFn, watchEvery, watchSettle
	defer func() { radiumRootFn, wechatRunningFn, watchEvery, watchSettle = oldRoot, oldRun, oldEvery, oldSettle }()
	radiumRootFn = func() string { return root }
	watchEvery, watchSettle = 20*time.Millisecond, 5*time.Millisecond
	var running atomic.Bool
	running.Store(true)
	wechatRunningFn = running.Load
	p := filepath.Join(root, "users", "wxid_a", "xworker", "liteapp", "local", "packages", "pkg", "engineConfig", "174", "dist", "engineConfig.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	on := `{"featureSwitch":{"handleFinder":true}}`
	os.WriteFile(p, []byte(on), 0o644)
	isOn := func() bool { b, _ := os.ReadFile(p); return reHandleFinderOn.Match(b) }

	l := &liteSwitcher{}
	go l.watch()
	time.Sleep(100 * time.Millisecond)
	if !isOn() {
		t.Fatal("微信开着时不该改它的文件")
	}
	running.Store(false) // 用户退出微信
	waitFor(t, func() bool { return !isOn() }, "微信退出后应该自动改成网页模式")
	if _, err := os.Stat(p + ".sphdl-bak"); err != nil {
		t.Fatal("改之前要备份")
	}
	// 微信重新打开、升级后又改回自带播放器，再退出：又自动改回来
	running.Store(true)
	time.Sleep(60 * time.Millisecond)
	os.WriteFile(p, []byte(on), 0o644)
	running.Store(false)
	waitFor(t, func() bool { return !isOn() }, "微信再次退出后应该再改回网页模式")
}

func waitFor(t *testing.T, ok func() bool, msg string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}
