package main

import (
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// DEMO=1 时只启动界面（假数据），用来截图看排版，不碰系统设置
func TestUIDemo(t *testing.T) {
	if os.Getenv("DEMO") != "1" {
		t.Skip()
	}
	dataDir = t.TempDir()
	s := NewStore("/Users/demo/Downloads/视频号下载")
	_ = s
	now := time.Now()
	cov := "http://127.0.0.1:18899/cover.jpg"
	add := func(it Item, ago int) {
		it.Seen = now.Add(-time.Duration(ago) * time.Second)
		it.FeedInfo.ID = it.Key
		if it.Cover == "" {
			it.Cover = cov
		}
		s.items[it.Key] = &it
	}
	add(Item{Key: "1", Source: "sniff", Status: "downloading", Progress: 23 << 20, Total: 61 << 20, FeedInfo: FeedInfo{Title: "三个两亿像素？OPPO Find X10 Pro Max上手 #科技 #数码", Nickname: "影视飓风MediaStorm", Duration: 583, Width: 1280, Height: 720, URL: "u"}}, 1)
	add(Item{Key: "2", Source: "sniff", Status: "done", File: "/Users/demo/Downloads/视频号下载/影视飓风MediaStorm_为什么是最后一期？_20261112.mp4", CoverFile: "x.jpg", Total: 76 << 20, FeedInfo: FeedInfo{Title: "为什么是最后一期？影视飓风1800万粉丝Q&A！#日常", Nickname: "影视飓风MediaStorm", Duration: 1133, URL: "u"}}, 2)
	add(Item{Key: "link:sph:AuQGcNgaw", Source: "link", Status: "waiting", Link: "https://weixin.qq.com/sph/AuQGcNgaw", FeedInfo: FeedInfo{Title: "韩博士说，在扶鹰一年零 8 个月带给我的这个成长收获，比我在英国 8 年留学带给我的成长收获要大得多得多", Nickname: "王金海聊家庭教育"}}, 3)
	add(Item{Key: "3", Source: "sniff", Status: "seen", FeedInfo: FeedInfo{Title: "飞机到底是怎么洗的？我们拍到了飞机清洗全过程！#飞机#航空", Nickname: "影视飓风MediaStorm", Duration: 412, Size: 45 << 20, URL: "u"}}, 4)
	add(Item{Key: "4", Source: "link", Status: "error", Err: "微信没有打开这条视频（视频号窗口要一直开着），点“下载”重试", FeedInfo: FeedInfo{Title: "让百万人尖叫的鬼屋？我们发现了这里的秘密", Nickname: "影视飓风MediaStorm"}}, 5)
	add(Item{Key: "5", Source: "sniff", Status: "seen", FeedInfo: FeedInfo{Title: "拿到新手机0.01秒后……", Nickname: "小绵羊是个好人", Duration: 10, Size: 1 << 20, URL: "u"}}, 6)
	s.pageSeen = now.Add(time.Hour)
	logf("已启动。保存位置：/Users/demo/Downloads/视频号下载")
	logf("视频号页面已连上：https://channels.weixin.qq.com/web/pages/home")
	cfg := config{}
	mux := http.NewServeMux()
	mux.HandleFunc("/cover.jpg", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, os.Getenv("COVER")) })
	mux.Handle("/", uiHandler(s, &cfg))
	ln, _ := net.Listen("tcp", "127.0.0.1:18899")
	go http.Serve(ln, mux)
	time.Sleep(25 * time.Second)
}
