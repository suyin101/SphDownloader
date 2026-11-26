package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDouyinLinks(t *testing.T) {
	text := `2.87 复制打开抖音，看看【AC艾瑞巴蒂的作品】中国男篮两个对手阵容，沙特5号12号依旧是强点，黎... https://v.douyin.com/Lyr1v9983ag/ u@S.LJ :3pm lcN:/ 06/27 2.87 复制打开抖音… https://v.douyin.com/Lyr1v9983ag/ u@S.LJ
https://www.douyin.com/video/7712324527477347506 https://www.douyin.com/jingxuan?modal_id=7639599472460626801
https://www.iesdouyin.com/share/note/7600000000000000001/?from=x`
	refs := parseDouyin(text)
	keys := []string{}
	for _, r := range refs {
		keys = append(keys, r.Key)
	}
	want := []string{"dy:7712324527477347506", "dy:7600000000000000001", "dy:7639599472460626801", "dy:s:Lyr1v9983ag"}
	if len(keys) != len(want) {
		t.Fatalf("识别不对：%v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("识别不对：%v", keys)
		}
	}
	if len(parseWebRefs("https://xhslink.cn/o/8c0riArKjjH https://v.douyin.com/Lyr1v9983ag/ https://mp.weixin.qq.com/s/abc")) != 3 {
		t.Fatal("三个平台混在一起没全认出来")
	}
}

// DY_JSON=保存的详情 JSON 文件
func TestParseDouyinDetail(t *testing.T) {
	f := os.Getenv("DY_JSON")
	if f == "" {
		t.Skip()
	}
	b, _ := os.ReadFile(f)
	p, err := parseDouyinDetail([]byte(`{"aweme_detail":` + string(b) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("标题=%s 作者=%s %dx%d %d秒 图=%d\n视频=%v", p.Title, p.Nickname, p.Width, p.Height, p.Duration, len(p.Images), p.Videos)
	if p.Width != 1080 || len(p.Videos) == 0 {
		t.Fatal("没选到最清晰的 h264")
	}
}

func TestDouyinLive(t *testing.T) {
	text := os.Getenv("DY_LIVE")
	if text == "" {
		t.Skip()
	}
	dataDir = t.TempDir()
	out := os.Getenv("OUT")
	if out == "" {
		out = t.TempDir()
	}
	s := NewStore(out)
	if s.AddLinks(text) == 0 {
		t.Fatal("没识别出链接")
	}
	deadline := time.Now().Add(3 * time.Minute)
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
				t.Logf("%s [%s] %s｜%s %dx%d 图%d 文件=%s 错误=%s", it.Key, it.Status, it.Nickname, it.Title, it.Width, it.Height, it.Images, filepath.Base(it.File), it.Err)
				if it.Status != "done" {
					t.Errorf("没成功：%s", it.Err)
				}
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("超时")
}
