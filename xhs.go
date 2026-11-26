package main

// xhs.go 小红书笔记：分享链接（xhslink.cn/o/xxx、xiaohongshu.com/discovery/item|explore/<id>?xsec_token=…）
// 打开的网页里 window.__INITIAL_STATE__ 带着笔记的标题、正文、作者、图片和视频地址，不用登录。
//   图片：取 fileId 拼 sns-img-bd.xhscdn.com/<fileId>，是上传的原图（无水印，实测 1760×2410 PNG）；
//   视频：media.stream.h264 里最清晰的一档，masterUrl 有时 403，backupUrls（sns-bak-*）能下。
// 视频笔记走和视频号一样的下载/预览/转文字；图文笔记和公众号一样存成图片文件夹（另存一份“正文.txt”）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reXHSShort = regexp.MustCompile(`https?://xhslink\.(?:cn|com)/[A-Za-z0-9/_-]+`)
	reXHSLong  = regexp.MustCompile(`https?://(?:www\.)?xiaohongshu\.com/(?:discovery/item|explore)/[0-9a-f]{24}[^\s"'<>，。]*`)
	reXHSID    = regexp.MustCompile(`/(?:discovery/item|explore)/([0-9a-f]{24})`)
	reXHSState = regexp.MustCompile(`(?s)window\.__INITIAL_STATE__\s*=\s*(\{.*?\})\s*</script>`)
	reUndef    = regexp.MustCompile(`([:\[,])\s*undefined\b`)
)

type xhsRef struct {
	URL string
	Key string // xhs:<笔记ID>（短链在解析前是 xhs:s:<短码>）
}

func parseXHS(text string) []xhsRef {
	var out []xhsRef
	seen := map[string]bool{}
	add := func(u, key string) {
		if !seen[key] {
			seen[key] = true
			out = append(out, xhsRef{URL: u, Key: key})
		}
	}
	for _, m := range reXHSLong.FindAllString(text, -1) {
		if id := reXHSID.FindStringSubmatch(m); id != nil {
			add(strings.ReplaceAll(m, "&amp;", "&"), "xhs:"+id[1])
		}
	}
	for _, m := range reXHSShort.FindAllString(text, -1) {
		m = strings.TrimRight(m, "/")
		add(m, "xhs:s:"+m[strings.LastIndex(m, "/")+1:])
	}
	return out
}

// webPost 从网页解析出来的一条内容（小红书，以后抖音也用）。
type webPost struct {
	ID         string
	Title      string
	Desc       string
	Nickname   string
	Cover      string
	CreateTime int64
	Images     []string // 图文：原图地址
	Videos     []string // 视频：依次尝试的地址
	Width      int
	Height     int
	Duration   int
}

const mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"

func fetchXHS(link string) (*webPost, error) {
	req, _ := http.NewRequest("GET", link, nil)
	req.Header.Set("User-Agent", mobileUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	c := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("打不开小红书链接：%v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	p, err := parseXHSPage(string(b))
	if err != nil {
		final := resp.Request.URL
		if final.Query().Get("xsec_token") == "" && strings.Contains(final.Host, "xiaohongshu.com") {
			return nil, errors.New("链接里缺少 xsec_token，请在小红书 App 里点“分享 → 复制链接”再粘贴")
		}
		tracef("[小红书] 解析失败 %s：%v", final, err)
		return nil, err
	}
	return p, nil
}

func parseXHSPage(html string) (*webPost, error) {
	m := reXHSState.FindStringSubmatch(html)
	if m == nil {
		if strings.Contains(html, "验证") || strings.Contains(html, "captcha") {
			return nil, errors.New("小红书要求验证，过一会儿再试")
		}
		return nil, errors.New("小红书页面里没有笔记数据（笔记可能已删除或设为私密）")
	}
	var root any
	if err := json.Unmarshal([]byte(reUndef.ReplaceAllString(m[1], "${1}null")), &root); err != nil {
		return nil, fmt.Errorf("小红书数据格式变了：%v", err)
	}
	note := findNote(root)
	if note == nil {
		return nil, errors.New("小红书页面里没有笔记数据（笔记可能已删除或设为私密）")
	}
	p := &webPost{ID: str(note["noteId"]), Title: strings.TrimSpace(str(note["title"])), Desc: cleanXHSDesc(str(note["desc"]))}
	if u, ok := note["user"].(map[string]any); ok {
		p.Nickname = str(u["nickName"])
		if p.Nickname == "" {
			p.Nickname = str(u["nickname"])
		}
	}
	if t := num(note["time"]); t > 0 {
		p.CreateTime = int64(t) / 1000
	}
	var imgs []map[string]any
	if list, ok := note["imageList"].([]any); ok {
		for _, x := range list {
			if im, ok := x.(map[string]any); ok {
				imgs = append(imgs, im)
			}
		}
	}
	for _, im := range imgs {
		if u := xhsOriginal(im); u != "" {
			p.Images = append(p.Images, u)
		}
	}
	if len(imgs) > 0 {
		p.Cover = httpsURL(str(imgs[0]["url"]))
		if p.Cover == "" && len(p.Images) > 0 {
			p.Cover = p.Images[0]
		}
	}
	if str(note["type"]) == "video" {
		v, _ := note["video"].(map[string]any)
		p.Videos, p.Width, p.Height = xhsVideoURLs(v)
		if capa, ok := v["capa"].(map[string]any); ok {
			p.Duration = int(num(capa["duration"]))
		}
		if len(p.Videos) == 0 {
			return nil, errors.New("小红书视频笔记里没找到视频地址")
		}
		p.Images = nil // 视频笔记的图片只是封面
	}
	if p.Title == "" {
		p.Title = firstLine(p.Desc, 40)
	}
	if p.Title == "" {
		p.Title = "小红书笔记"
	}
	if len(p.Images) == 0 && len(p.Videos) == 0 {
		return nil, errors.New("这条小红书笔记里没有图片或视频")
	}
	return p, nil
}

// findNote 在整棵数据里找“像笔记”的对象：有 noteId，还有 imageList 或 type。手机页、电脑页结构不同，这样都能找到。
func findNote(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["noteId"]; ok {
			if _, ok := x["imageList"]; ok {
				return x
			}
		}
		for _, k := range []string{"noteData", "note", "noteDetailMap", "data"} { // 先找常见位置
			if c, ok := x[k]; ok {
				if r := findNote(c); r != nil {
					return r
				}
			}
		}
		for _, c := range x {
			if r := findNote(c); r != nil {
				return r
			}
		}
	case []any:
		for _, c := range x {
			if r := findNote(c); r != nil {
				return r
			}
		}
	}
	return nil
}

// xhsOriginal 原图：fileId（或网页图地址里的那段 key）拼到 sns-img-bd 上。
func xhsOriginal(im map[string]any) string {
	id := str(im["fileId"])
	if id == "" {
		id = str(im["traceId"])
	}
	if id == "" {
		// http://sns-webpic-qc.xhscdn.com/<时间>/<签名>/<key>!h5_1080jpg
		u := str(im["url"])
		if i := strings.Index(u, "xhscdn.com/"); i >= 0 {
			parts := strings.SplitN(u[i+len("xhscdn.com/"):], "/", 3)
			if len(parts) == 3 {
				id = strings.SplitN(parts[2], "!", 2)[0]
			}
		}
	}
	if id == "" {
		return httpsURL(str(im["url"]))
	}
	return "https://sns-img-bd.xhscdn.com/" + id
}

func xhsVideoURLs(v map[string]any) ([]string, int, int) {
	media, _ := v["media"].(map[string]any)
	stream, _ := media["stream"].(map[string]any)
	var best map[string]any
	for _, codec := range []string{"h264", "h265", "av1"} { // h264 兼容性最好，优先
		list, _ := stream[codec].([]any)
		for _, x := range list {
			s, ok := x.(map[string]any)
			if !ok || (str(s["masterUrl"]) == "" && s["backupUrls"] == nil) {
				continue
			}
			if best == nil || num(s["width"])*num(s["height"]) > num(best["width"])*num(best["height"]) {
				best = s
			}
		}
		if best != nil {
			break
		}
	}
	if best == nil {
		return nil, 0, 0
	}
	var urls []string
	if u := str(best["masterUrl"]); u != "" {
		urls = append(urls, u)
	}
	if list, ok := best["backupUrls"].([]any); ok {
		for _, x := range list {
			if u := str(x); u != "" {
				urls = append(urls, u)
			}
		}
	}
	return urls, int(num(best["width"])), int(num(best["height"]))
}

var reXHSTopic = regexp.MustCompile(`\[话题\]#`)

func cleanXHSDesc(s string) string {
	s = reXHSTopic.ReplaceAllString(s, "#")
	return strings.TrimSpace(s)
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

func httpsURL(u string) string {
	if strings.HasPrefix(u, "http://") && strings.Contains(u, "xhscdn.com") {
		return "https://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// applyWebPost 把解析结果填进列表条目。
func (it *Item) applyWebPost(p *webPost, prefix string) {
	it.Title, it.Nickname, it.CreateTime, it.Cover = p.Title, p.Nickname, p.CreateTime, p.Cover
	it.Desc = p.Desc
	if len(p.Videos) > 0 {
		// masterUrl 实测有时 403，备用地址（sns-bak-*）能下：备用的排前面，预览也用第一个
		urls := append(append([]string{}, p.Videos[1:]...), p.Videos[0])
		it.URL, it.alt = urls[0], urls[1:]
		it.Width, it.Height, it.Duration = p.Width, p.Height, p.Duration
		it.images, it.Images = nil, 0
	} else {
		it.images, it.Images = p.Images, len(p.Images)
	}
	if p.ID != "" {
		it.ID = prefix + p.ID
	}
}
