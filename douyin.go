package main

// douyin.go 抖音（做法 B）：用软件自带的浏览器组件在隐藏窗口里正常打开 www.douyin.com/video/<id>，
// douyin_hook.js 旁听网页自己请求到的 /aweme/v1/web/aweme/detail/ 数据（签名、Cookie 都由抖音网页自己处理），
// 从里面取无水印视频（bit_rate 里最清晰的 h264 档 play_addr）或图文原图。下载视频要带 Referer: https://www.douyin.com/。
// 2026-11-26 用 Electron 实测：不登录约 12 秒拿到数据，1080×1440 h264 无水印。
// 浏览器部分按平台：douyin_darwin.go（WKWebView）/ douyin_windows.go（WebView2）。

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

//go:embed douyin_hook.js
var douyinHookJS string

var (
	reDYShort = regexp.MustCompile(`https?://v\.douyin\.com/[A-Za-z0-9_-]+`)
	reDYLong  = regexp.MustCompile(`https?://(?:www\.)?(?:ies)?douyin\.com/(?:share/)?(?:video|note|slides)/(\d{15,22})`)
	reDYModal = regexp.MustCompile(`https?://(?:www\.)?douyin\.com/[^\s"'<>]*[?&](?:modal_id|vid)=(\d{15,22})`)
	reDYID    = regexp.MustCompile(`/(?:video|note|slides)/(\d{15,22})`)
)

func parseDouyin(text string) []webRef {
	var out []webRef
	seen := map[string]bool{}
	add := func(u, key string) {
		if !seen[key] {
			seen[key] = true
			out = append(out, webRef{u, key, "douyin"})
		}
	}
	for _, re := range []*regexp.Regexp{reDYLong, reDYModal} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			add("https://www.douyin.com/video/"+m[1], "dy:"+m[1])
		}
	}
	for _, m := range reDYShort.FindAllString(text, -1) {
		add(m+"/", "dy:s:"+m[strings.LastIndex(m, "/")+1:])
	}
	return out
}

// douyinID 短链接只取跳转地址里的作品 ID（不打开网页）。
func douyinID(link string) (string, error) {
	if m := reDYID.FindStringSubmatch(link); m != nil {
		return m[1], nil
	}
	c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u := link
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", mobileUA)
		resp, err := c.Do(req)
		if err != nil {
			return "", fmt.Errorf("打不开抖音链接：%v", err)
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if m := reDYID.FindStringSubmatch(loc); m != nil {
			return m[1], nil
		}
		if loc == "" {
			break
		}
		u = loc
	}
	return "", errors.New("没从抖音链接里找到作品，链接可能失效了")
}

// browserFetchDouyin 由各平台实现：隐藏窗口打开页面，返回网页拿到的详情 JSON。
var douyinBusy = make(chan struct{}, 1) // 同一时间只开一个隐藏网页

func fetchDouyin(link string) (*webPost, error) {
	id, err := douyinID(link)
	if err != nil {
		return nil, err
	}
	douyinBusy <- struct{}{}
	defer func() { <-douyinBusy }()
	start := time.Now()
	raw, err := browserFetchDouyin(id, strings.ReplaceAll(douyinHookJS, "__ID__", id))
	if err != nil {
		return nil, err
	}
	p, err := parseDouyinDetail([]byte(raw))
	if err == nil {
		tracef("[抖音] %s 用时 %v", id, time.Since(start).Round(time.Millisecond))
	}
	return p, err
}

func parseDouyinDetail(b []byte) (*webPost, error) {
	var root struct {
		Detail map[string]any `json:"aweme_detail"`
	}
	if err := json.Unmarshal(b, &root); err != nil || root.Detail == nil {
		return nil, errors.New("抖音没返回作品数据（作品可能已删除或设为私密）")
	}
	a := root.Detail
	p := &webPost{ID: str(a["aweme_id"]), Desc: strings.TrimSpace(str(a["desc"]))}
	p.Title = firstLine(p.Desc, 50)
	if au, ok := a["author"].(map[string]any); ok {
		p.Nickname = str(au["nickname"])
	}
	p.CreateTime = int64(num(a["create_time"]))
	v, _ := a["video"].(map[string]any)
	if v != nil {
		p.Duration = int(num(v["duration"]) / 1000)
		if c, ok := v["cover"].(map[string]any); ok {
			p.Cover = firstURL(c)
		}
	}
	if imgs, ok := a["images"].([]any); ok && len(imgs) > 0 { // 图文
		for _, x := range imgs {
			im, _ := x.(map[string]any)
			if u := pickDouyinImage(im); u != "" {
				p.Images = append(p.Images, u)
			}
		}
		if p.Cover == "" && len(p.Images) > 0 {
			p.Cover = p.Images[0]
		}
	} else if v != nil {
		p.Videos, p.Width, p.Height = douyinVideoURLs(v)
		if len(p.Videos) == 0 {
			return nil, errors.New("抖音作品里没找到视频地址")
		}
	}
	if p.Title == "" {
		p.Title = "抖音作品"
	}
	if len(p.Images) == 0 && len(p.Videos) == 0 {
		return nil, errors.New("这个抖音作品里没有视频或图片")
	}
	return p, nil
}

// douyinVideoURLs 最清晰的 h264 档（h265 部分电脑播不了）；没有就用默认的 play_addr。play_addr 本身就是无水印的。
func douyinVideoURLs(v map[string]any) ([]string, int, int) {
	var best map[string]any
	if rates, ok := v["bit_rate"].([]any); ok {
		for _, x := range rates {
			r, _ := x.(map[string]any)
			pa, _ := r["play_addr"].(map[string]any)
			if pa == nil || num(r["is_h265"]) == 1 || len(urlList(pa)) == 0 {
				continue
			}
			if best == nil || num(pa["width"])*num(pa["height"]) > num(best["width"])*num(best["height"]) ||
				(num(pa["width"])*num(pa["height"]) == num(best["width"])*num(best["height"]) && num(pa["data_size"]) > num(best["data_size"])) {
				best = pa
			}
		}
	}
	if best == nil {
		best, _ = v["play_addr"].(map[string]any)
	}
	if best == nil {
		return nil, 0, 0
	}
	w, h := int(num(best["width"])), int(num(best["height"]))
	if w == 0 {
		w, h = int(num(v["width"])), int(num(v["height"]))
	}
	return urlList(best), w, h
}

func pickDouyinImage(im map[string]any) string {
	list := urlList(im) // url_list 无水印；download_url_list 带水印，不用
	for _, u := range list {
		if strings.Contains(u, ".jpeg") || strings.Contains(u, ".jpg") {
			return u
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return ""
}

func urlList(m map[string]any) []string {
	var out []string
	if l, ok := m["url_list"].([]any); ok {
		for _, x := range l {
			if u := str(x); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

func firstURL(m map[string]any) string {
	if l := urlList(m); len(l) > 0 {
		return l[0]
	}
	return ""
}

// needReferer 抖音的视频、图片地址不带 Referer 会 403。
func setReferer(req *http.Request) {
	h := req.URL.Host
	if strings.Contains(h, "douyin") || strings.Contains(h, "douyinvod") || strings.Contains(h, "douyinpic") || strings.Contains(h, "bytecdn") || strings.Contains(h, "zjcdn") {
		req.Header.Set("Referer", "https://www.douyin.com/")
	}
}

var errNoWebView2 = errors.New("这台电脑没有 WebView2 组件，解析不了抖音（装一下微软 Edge 浏览器就有了）")

// waitDouyin 等网页把数据交过来。抖音要求验证时把窗口显示出来，等用户手动完成。
func waitDouyin(ch chan string, show func()) (string, error) {
	timeout := time.NewTimer(40 * time.Second)
	defer timeout.Stop()
	shown, seen := false, 0
	for {
		select {
		case msg := <-ch:
			if strings.HasPrefix(msg, `{"__captcha"`) {
				seen++ // 连续 3 次（约 6 秒）都还挂着验证框、也没拿到数据，才弹窗
				if !shown && seen >= 3 {
					shown = true
					logf("抖音要求验证：请在弹出的窗口里完成验证，完成后会自动继续")
					show()
					timeout.Reset(3 * time.Minute)
				}
				continue
			}
			return msg, nil
		case <-timeout.C:
			if !shown {
				return "", errors.New("抖音网页 40 秒内没加载出作品数据，稍后点“下载”重试")
			}
			return "", errors.New("等验证超时了，点“下载”重试")
		}
	}
}
