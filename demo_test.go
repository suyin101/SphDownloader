package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	vdir := t.TempDir()
	v2 := filepath.Join(vdir, "影视飓风MediaStorm_为什么是最后一期？_20261112.mp4")
	os.WriteFile(v2, []byte("x"), 0o644)
	writeTranscript(txtFor(v2), FeedInfo{Title: "为什么是最后一期？影视飓风1800万粉丝Q&A！", Nickname: "影视飓风MediaStorm", CreateTime: 1762900000}, 1133,
		"哈喽大家好，欢迎来到影视飓风，今天是我们一年一度的粉丝问答。这一期呢，我们收集了大家在评论区里问得最多的问题，一个一个来回答。\n\n首先第一个问题，为什么说这是最后一期？其实不是频道要停更了，而是这个栏目的形式我们想换一换。\n\n第二个问题，很多人问我们平时用什么设备拍摄，这个我们后面会专门出一期视频来讲。")
	if real := os.Getenv("REAL_MP4"); real != "" { // 没封面（自带播放器模式）的视频：缩略图从视频里截一帧
		add(Item{Key: "8", Source: "lite", Status: "done", File: real, Total: 12 << 20, FeedInfo: FeedInfo{Title: "视频号视频 11-26 17:20", Duration: 74, URL: "u"}}, 0)
		s.items["8"].Cover = ""
	}
	add(Item{Key: "6", Source: "link", Status: "done", Tx: "running", TxPct: 45, File: v2 + "x", Total: 12 << 20, FeedInfo: FeedInfo{Title: "孩子越催越摆烂？父母到底应该怎么办？", Nickname: "扶鹰王金海家庭教育", Duration: 74, URL: "u"}}, 0)
	add(Item{Key: "7", Source: "sniff", Status: "done", Tx: "error", TxErr: "没识别到人声（可能是纯音乐或只有花字）", File: v2 + "y", Total: 2 << 20, FeedInfo: FeedInfo{Title: "无标题", Nickname: "小涵宝同学", Duration: 8, URL: "u"}}, 0)
	add(Item{Key: "2", Source: "sniff", Status: "done", File: v2, CoverFile: "x.jpg", Total: 76 << 20, FeedInfo: FeedInfo{Title: "为什么是最后一期？影视飓风1800万粉丝Q&A！#日常", Nickname: "影视飓风MediaStorm", Duration: 1133, URL: "u"}}, 2)
	add(Item{Key: "link:sph:AuQGcNgaw", Source: "link", Status: "waiting", Link: "https://weixin.qq.com/sph/AuQGcNgaw", FeedInfo: FeedInfo{Title: "韩博士说，在扶鹰一年零 8 个月带给我的这个成长收获，比我在英国 8 年留学带给我的成长收获要大得多得多", Nickname: "王金海聊家庭教育"}}, 3)
	add(Item{Key: "3", Source: "sniff", Status: "seen", FeedInfo: FeedInfo{Title: "飞机到底是怎么洗的？我们拍到了飞机清洗全过程！#飞机#航空", Nickname: "影视飓风MediaStorm", Duration: 412, Size: 45 << 20, URL: "u"}}, 4)
	add(Item{Key: "4", Source: "link", Status: "error", Err: "微信没有打开这条视频（视频号窗口要一直开着），点“下载”重试", FeedInfo: FeedInfo{Title: "让百万人尖叫的鬼屋？我们发现了这里的秘密", Nickname: "影视飓风MediaStorm"}}, 5)
	add(Item{Key: "5", Source: "sniff", Status: "seen", FeedInfo: FeedInfo{Title: "拿到新手机0.01秒后……", Nickname: "小绵羊是个好人", Duration: 10, Size: 1 << 20, URL: "u"}}, 6)
	s.pageSeen = now.Add(time.Hour)
	s.items["1"].Opened = now
	s.items["2"].Opened = now.Add(-time.Minute)
	s.currentKey = "1"
	logf("已启动。保存位置：/Users/demo/Downloads/视频号下载")
	logf("视频号页面已连上：https://channels.weixin.qq.com/web/pages/home")
	s.tx = &Transcriber{dir: t.TempDir(), st: ASRState{Status: "downloading", Progress: 112 << 20, Total: 248 << 20}}
	if os.Getenv("DEMO") == "1" && os.Getenv("ASR_READY") == "1" {
		s.tx.st.Status = "ready"
	}
	cfg := config{}
	mux := http.NewServeMux()
	mux.HandleFunc("/cover.jpg", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, os.Getenv("COVER")) })
	ui := uiHandler(s, &cfg)
	mux.HandleFunc("/dlg", func(w http.ResponseWriter, r *http.Request) { // 自动打开文案窗口，截图用
		w.Write(bytes.Replace(uiHTML, []byte("</body>"), []byte(`<script>setTimeout(function(){document.querySelector('[data-act=text]').click()},800)</script></body>`), 1))
	})
	mux.HandleFunc("/sel", func(w http.ResponseWriter, r *http.Request) { // 自动勾选两条，截图看“转文字”按钮
		w.Write(bytes.Replace(uiHTML, []byte("</body>"), []byte(`<script>setTimeout(function(){['7','2'].forEach(function(k){var c=document.querySelector('[data-sel="'+k+'"]');c.checked=true;c.dispatchEvent(new Event('change',{bubbles:true}))})},800)</script></body>`), 1))
	})
	mux.Handle("/", ui)
	ln, _ := net.Listen("tcp", "127.0.0.1:18899")
	go http.Serve(ln, mux)
	secs := 25
	if v, _ := strconv.Atoi(os.Getenv("DEMO_SECS")); v > 0 {
		secs = v
	}
	time.Sleep(time.Duration(secs) * time.Second)
}
