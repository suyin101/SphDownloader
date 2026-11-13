package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// 支持的链接：
//
//	https://weixin.qq.com/sph/AuQGcNgaw
//	https://channels.weixin.qq.com/finder-preview/pages/sph?id=AuQGcNgaw
//	https://channels.weixin.qq.com/web/pages/feed?eid=export/xxxx
var (
	reShort   = regexp.MustCompile(`weixin\.qq\.com/sph/([A-Za-z0-9_-]+)`)
	rePreview = regexp.MustCompile(`finder-preview/pages/sph\?[^\s"']*?\bid=([A-Za-z0-9_-]+)`)
	reEid     = regexp.MustCompile(`[?&]eid=([^&\s"']+)`)
)

type linkRef struct {
	Raw      string
	ShortURI string
	ExportID string
}

func (l linkRef) key() string {
	if l.ShortURI != "" {
		return "sph:" + l.ShortURI
	}
	return "eid:" + l.ExportID
}

// parseLinks 从一段文字里找出所有视频号链接（可以直接粘贴微信里复制的整段分享文字）。
func parseLinks(text string) []linkRef {
	var out []linkRef
	seen := map[string]bool{}
	add := func(l linkRef) {
		if !seen[l.key()] {
			seen[l.key()] = true
			out = append(out, l)
		}
	}
	for _, m := range reShort.FindAllStringSubmatch(text, -1) {
		add(linkRef{Raw: "https://weixin.qq.com/sph/" + m[1], ShortURI: m[1]})
	}
	for _, m := range rePreview.FindAllStringSubmatch(text, -1) {
		add(linkRef{Raw: m[0], ShortURI: m[1]})
	}
	for _, m := range reEid.FindAllStringSubmatch(text, -1) {
		if eid, err := url.QueryUnescape(m[1]); err == nil && strings.HasPrefix(eid, "export/") {
			add(linkRef{Raw: m[0], ExportID: eid})
		}
	}
	return out
}

type linkInfo struct {
	Title, Nickname, Cover string
	CreateTime             int64
	ExportID               string
	FeedURL                string
}

var apiClient = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}

// resolveLink 调用视频号公开的预览接口，拿到标题、作者、封面和视频 ID（不需要登录）。
func resolveLink(l linkRef) (*linkInfo, error) {
	body := map[string]any{"baseReq": map[string]string{"generalToken": ""}}
	referer := "https://channels.weixin.qq.com/finder-preview/pages/feed?eid=" + url.QueryEscape(l.ExportID)
	if l.ShortURI != "" {
		body["shortUri"] = l.ShortURI
		referer = "https://channels.weixin.qq.com/finder-preview/pages/sph?id=" + l.ShortURI
	} else {
		body["exportId"] = l.ExportID
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://channels.weixin.qq.com/finder-preview/api/feed/get_feed_info", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://channels.weixin.qq.com")
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36")
	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络错误：%v", err)
	}
	defer resp.Body.Close()
	var r struct {
		ErrCode int    `json:"errCode"`
		ErrMsg  string `json:"errMsg"`
		Data    struct {
			AuthorInfo struct {
				Nickname string `json:"nickname"`
			} `json:"authorInfo"`
			FeedInfo struct {
				Description string `json:"description"`
				CoverURL    string `json:"coverUrl"`
				CreateTime  int64  `json:"createtime"`
			} `json:"feedInfo"`
			ErrMsg *struct {
				Type    int    `json:"type"`
				Title   string `json:"title"`
				Content string `json:"content"`
			} `json:"errMsg"`
			SceneInfo struct {
				DynamicExportID string `json:"dynamicExportId"`
				CommentScene    int    `json:"commentScene"`
				EntryScene      int    `json:"entryScene"`
				EntryCardType   int    `json:"entryCardType"`
			} `json:"sceneInfo"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("接口返回格式不对（HTTP %d）", resp.StatusCode)
	}
	if r.ErrCode != 0 {
		return nil, fmt.Errorf("链接无效（%d %s）", r.ErrCode, r.ErrMsg)
	}
	if e := r.Data.ErrMsg; e != nil && e.Type != 0 {
		return nil, fmt.Errorf("%s %s", e.Title, e.Content)
	}
	eid := r.Data.SceneInfo.DynamicExportID
	if eid == "" {
		eid = l.ExportID
	}
	if eid == "" {
		return nil, fmt.Errorf("没拿到视频 ID，链接可能已失效")
	}
	s := r.Data.SceneInfo
	ctx := fmt.Sprintf("%d-%d-%d-%d", s.EntryScene, s.EntryCardType, s.CommentScene, time.Now().UnixMilli())
	feedURL := "https://channels.weixin.qq.com/web/pages/feed?eid=" + url.QueryEscape(eid) +
		"&context_id=" + ctx + "&entrance_id=1019"
	return &linkInfo{
		Title:      r.Data.FeedInfo.Description,
		Nickname:   r.Data.AuthorInfo.Nickname,
		Cover:      r.Data.FeedInfo.CoverURL,
		CreateTime: r.Data.FeedInfo.CreateTime,
		ExportID:   eid,
		FeedURL:    feedURL,
	}, nil
}
