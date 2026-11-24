package main

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 公众号文章图片下载（无水印原图）。
//
// 小绿书（图片消息，item_show_type=8）：页面里 picture_page_info_list 每张图有两个地址——
// 第一个 cdn_url 是原图，watermark_info.cdn_url 是加了「公众号 · 名字」水印的版本，下第一个就没有水印。
// 普通长文章：正文 #js_content 里 img 的 data-src，把 /640? 换成 /0? 就是原尺寸。
// 页面和图片都不用登录、不查来源，直接下载就行（2026-11-24 实测）。

var reArticle = regexp.MustCompile(`https?://mp\.weixin\.qq\.com/s(?:/[A-Za-z0-9_-]+|\?[^\s"'<>]+)`)

type articleRef struct {
	URL string
	Key string // mp:短链ID 或 mp:biz-mid-idx
}

func parseArticles(text string) []articleRef {
	var out []articleRef
	seen := map[string]bool{}
	for _, m := range reArticle.FindAllString(text, -1) {
		raw := strings.ReplaceAll(m, "&amp;", "&")
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		var key string
		if id := strings.TrimPrefix(u.Path, "/s/"); id != u.Path && id != "" {
			key = "mp:" + id
			raw = "https://mp.weixin.qq.com/s/" + id
		} else {
			q := u.Query()
			if q.Get("__biz") == "" || q.Get("mid") == "" {
				continue
			}
			key = "mp:" + q.Get("__biz") + "-" + q.Get("mid") + "-" + q.Get("idx")
			nq := url.Values{}
			for _, k := range []string{"__biz", "mid", "idx", "sn"} {
				if v := q.Get(k); v != "" {
					nq.Set(k, v)
				}
			}
			raw = "https://mp.weixin.qq.com/s?" + nq.Encode()
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, articleRef{URL: raw, Key: key})
		}
	}
	return out
}

type articleInfo struct {
	Title, Nickname, Cover string
	CreateTime             int64
	Images                 []string
	Gallery                bool // 小绿书（图片消息）
}

var (
	reOgTitle   = regexp.MustCompile(`<meta property="og:title" content="([^"]*)"`)
	reOgImage   = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	reNick1     = regexp.MustCompile(`var nickname = htmlDecode\("([^"]*)"\)`)
	reNick2     = regexp.MustCompile(`nick_name: '([^']+)'`)
	reCT1       = regexp.MustCompile(`var ct = "(\d+)"`)
	reCT2       = regexp.MustCompile(`create_time: '(\d{9,})'`)
	reCdnURL    = regexp.MustCompile(`cdn_url['"]?\s*:\s*['"]([^'"]+)['"]`)
	reDataSrc   = regexp.MustCompile(`data-src="(https?://mmbiz\.qpic\.cn/[^"]+)"`)
	reImgSize   = regexp.MustCompile(`/\d+\?`)
	reNoArticle = regexp.MustCompile(`该内容已被发布者删除|此内容因违规无法查看|该内容暂时无法查看|环境异常`)
)

func fetchArticle(pageURL string) (*articleInfo, error) {
	req, _ := http.NewRequest("GET", pageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络错误：%v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 30<<20))
	if err != nil {
		return nil, fmt.Errorf("网络错误：%v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("打开文章失败（HTTP %d）", resp.StatusCode)
	}
	info := parseArticlePage(string(b))
	if len(info.Images) == 0 {
		if m := reNoArticle.FindString(string(b)); m != "" {
			return nil, fmt.Errorf("打不开这篇文章：%s", m)
		}
		return nil, fmt.Errorf("这篇文章里没找到图片")
	}
	return info, nil
}

func first(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func cleanImgURL(u string) string {
	u = strings.ReplaceAll(u, `\x26`, "&")
	u = html.UnescapeString(u)
	u = strings.Replace(u, "http://", "https://", 1)
	return u
}

func parseArticlePage(s string) *articleInfo {
	info := &articleInfo{
		Title:    strings.TrimSpace(html.UnescapeString(first(reOgTitle, s))),
		Nickname: html.UnescapeString(first(reNick1, s)),
		Cover:    cleanImgURL(first(reOgImage, s)),
	}
	if info.Nickname == "" {
		info.Nickname = first(reNick2, s)
	}
	ct := first(reCT1, s)
	if ct == "" {
		ct = first(reCT2, s)
	}
	info.CreateTime, _ = strconv.ParseInt(ct, 10, 64)

	seen := map[string]bool{}
	add := func(u string) {
		u = cleanImgURL(u)
		if u != "" && !seen[u] {
			seen[u] = true
			info.Images = append(info.Images, u)
		}
	}
	// 小绿书：每张图取它对象里第一个 cdn_url（原图），跳过 watermark_info / share_cover 里的
	for _, obj := range pictureObjects(s) {
		if m := reCdnURL.FindStringSubmatch(obj); m != nil {
			add(m[1])
		}
	}
	if len(info.Images) > 0 {
		info.Gallery = true
		return info
	}
	// 普通文章：正文里的图，换成原尺寸
	if i := strings.Index(s, `id="js_content"`); i >= 0 {
		body := s[i:]
		if j := strings.Index(body, "<script"); j > 0 {
			body = body[:j]
		}
		for _, m := range reDataSrc.FindAllStringSubmatch(body, -1) {
			add(reImgSize.ReplaceAllString(cleanImgURL(m[1]), "/0?"))
		}
	}
	return info
}

// pictureObjects 找到第一个 picture_page_info_list 数组，按括号层级切出每张图的 {...}。
func pictureObjects(s string) []string {
	i := strings.Index(s, "picture_page_info_list")
	for i >= 0 {
		rest := s[i+len("picture_page_info_list"):]
		j := strings.IndexAny(rest, "[=;")
		if j >= 0 && rest[j] == '[' && strings.TrimSpace(strings.Trim(rest[:j], `"':`)) == "" {
			return splitTopObjects(rest[j:])
		}
		k := strings.Index(rest, "picture_page_info_list")
		if k < 0 {
			return nil
		}
		i += len("picture_page_info_list") + k
	}
	return nil
}

func splitTopObjects(s string) []string {
	var out []string
	depth, start := 0, -1
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '{':
			depth++
			if c == '{' && depth == 2 {
				start = i
			}
		case ']', '}':
			depth--
			if c == '}' && depth == 1 && start >= 0 {
				out = append(out, s[start:i+1])
				start = -1
			}
			if depth == 0 {
				return out
			}
		}
	}
	return out
}

func articleDirName(f FeedInfo) string {
	return strings.TrimSuffix(fileName(f), ".mp4")
}

func imageExt(head []byte, u string) string {
	switch http.DetectContentType(head) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/jpeg":
		return ".jpg"
	}
	if strings.Contains(u, "wx_fmt=png") {
		return ".png"
	}
	return ".jpg"
}

// downloadArticle 把文章的图片按顺序存进「作者_标题_日期」文件夹，返回文件夹路径。
func (s *Store) downloadArticle(it *Item) (string, error) {
	s.mu.Lock()
	imgs, link, dir, f := it.images, it.Link, s.Dir, it.FeedInfo
	s.mu.Unlock()
	if len(imgs) == 0 { // 解析失败后点“下载”重试
		info, err := fetchArticle(link)
		if err != nil {
			return "", err
		}
		s.mu.Lock()
		it.applyArticle(info)
		imgs, f = it.images, it.FeedInfo
		s.mu.Unlock()
	}
	folder := uniquePath(filepath.Join(dir, articleDirName(f)))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	s.mu.Lock()
	it.Total, it.Progress = int64(len(imgs)), 0
	s.mu.Unlock()
	var failed int
	for i, u := range imgs {
		if err := saveImage(u, filepath.Join(folder, fmt.Sprintf("%02d", i+1))); err != nil {
			failed++
			logf("第 %d 张图下载失败：%v", i+1, err)
		}
		s.mu.Lock()
		it.Progress = int64(i + 1)
		s.mu.Unlock()
	}
	if failed == len(imgs) {
		os.Remove(folder)
		return "", fmt.Errorf("图片全部下载失败")
	}
	if failed > 0 {
		logf("%d 张图里有 %d 张没下成功，可以点“重新下载”", len(imgs), failed)
	}
	return folder, nil
}

func saveImage(u, base string) error {
	var resp *http.Response
	var err error
	for try := 0; try < 3; try++ {
		resp, err = client.Get(u)
		if err == nil && resp.StatusCode == 200 {
			break
		}
		if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp := base + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(resp.Body, head)
	head = head[:n]
	out.Write(head)
	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil || n == 0 {
		os.Remove(tmp)
		if err == nil {
			err = fmt.Errorf("图片是空的")
		}
		return err
	}
	return os.Rename(tmp, base+imageExt(head, u))
}

func (it *Item) applyArticle(info *articleInfo) {
	it.Title, it.Nickname, it.CreateTime = info.Title, info.Nickname, info.CreateTime
	it.Cover = info.Cover
	if it.Cover == "" && len(info.Images) > 0 {
		it.Cover = info.Images[0]
	}
	it.images = info.Images
	it.Images = len(info.Images)
	if it.Title == "" {
		it.Title = "公众号文章"
	}
}
