package main

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// 端到端：真的 inject.js 在假页面里生成上报 → 原样交给下载器的 handleReport → 列表第一行必须是最后刷到的那条。
func TestPageToListE2E(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("没有 node")
	}
	js := "inject.js"
	if v := os.Getenv("INJECT_JS"); v != "" {
		js = v
	}
	out, err := exec.Command("node", "testdata/page_sim.js", js).Output()
	if err != nil {
		t.Fatal(err)
	}
	dataDir = t.TempDir()
	s := NewStore(t.TempDir())
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<20), 20<<20)
	n := 0
	for sc.Scan() {
		r, _ := http.NewRequest("POST", "https://channels.weixin.qq.com/__sphdl/report", strings.NewReader(sc.Text()))
		handleReport(r, s)
		n++
	}
	items, _ := s.Snapshot()
	if len(items) != 60 {
		t.Fatalf("列表应该有 60 条，实际 %d（上报 %d 个）", len(items), n)
	}
	if items[0].Key != "140030" || s.CurrentKey() != "140030" {
		t.Fatalf("最后刷到的是第 30 条，第一行却是 %s（正在看 %s）", items[0].Key, s.CurrentKey())
	}
	for i, want := range []string{"140030", "140012", "14007", "14006", "14005"} {
		if items[i].Key != want {
			t.Fatalf("第 %d 行应该是 %s，实际 %s", i+1, want, items[i].Key)
		}
	}
	if items[0].Duration != 68 || items[0].CreateTime != 1795740000 {
		t.Fatalf("时长/时间没转对：%d %d", items[0].Duration, items[0].CreateTime)
	}
	_ = os.Stdout
}
