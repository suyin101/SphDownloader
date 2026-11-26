package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseXHSLinks(t *testing.T) {
	text := `你会喜欢目前这个 AI 所做的设计吗？ 我目前是一位软... https://xhslink.cn/o/3zHVYaAD5le 前往【小红书】一探究竟吧！
挡风被… https://xhslink.cn/o/8c0riArKjjH 复制一下，跳转【小红书】即刻浏览笔记。
https://www.xiaohongshu.com/discovery/item/6b03e6970000000019037197?app_platform=ios&xsec_token=CBA0q=&type=normal，
https://xhslink.cn/o/8c0riArKjjH`
	refs := parseXHS(text)
	if len(refs) != 3 || refs[0].Key != "xhs:6b03e6970000000019037197" || refs[1].Key != "xhs:s:3zHVYaAD5le" || refs[2].URL != "https://xhslink.cn/o/8c0riArKjjH" {
		t.Fatalf("链接识别不对：%+v", refs)
	}
	if strings.Contains(refs[0].URL, "，") {
		t.Fatal("链接后面的中文标点没去掉")
	}
	if len(parseWebRefs("https://weixin.qq.com/sph/AuQGcNgaw")) != 0 {
		t.Fatal("视频号链接不该当成网页链接")
	}
}

// 保存好的小红书网页：XHS_HTML=文件 go test -run TestParseXHSPage -v
func TestParseXHSPage(t *testing.T) {
	for _, f := range strings.Fields(os.Getenv("XHS_HTML")) {
		b, _ := os.ReadFile(f)
		p, err := parseXHSPage(string(b))
		if err != nil {
			t.Fatalf("%s：%v", f, err)
		}
		t.Logf("%s：标题=%s 作者=%s 图=%d 视频=%v %dx%d %d秒\n正文：%s", filepath.Base(f), p.Title, p.Nickname, len(p.Images), p.Videos, p.Width, p.Height, p.Duration, p.Desc)
		if p.Title == "" || p.Nickname == "" || (len(p.Images) == 0 && len(p.Videos) == 0) {
			t.Fatal("内容不全")
		}
	}
}

// 真实下载：LIVE_XHS="分享文字" go test -run TestXHSLive -v
func TestXHSLive(t *testing.T) {
	text := os.Getenv("LIVE_XHS")
	if text == "" {
		t.Skip()
	}
	dataDir = t.TempDir()
	out := os.Getenv("OUT")
	if out == "" {
		out = t.TempDir()
	}
	s := NewStore(out)
	if n := s.AddLinks(text); n == 0 {
		t.Fatal("没识别出链接")
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		items, _ := s.Snapshot()
		busy := 0
		for _, it := range items {
			if it.Status != "done" && it.Status != "error" {
				busy++
			}
		}
		if busy == 0 {
			for _, it := range items {
				t.Logf("%s [%s] %s 图%d 文件=%s 错误=%s", it.Key, it.Status, it.Title, it.Images, it.File, it.Err)
				if it.Status != "done" {
					t.Errorf("没下载成功：%s", it.Err)
				}
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("超时")
}
