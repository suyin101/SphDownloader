package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
)

//go:embed inject.js
var injectJS string

// 只解密这几个域名的流量，其它网站（包括微信聊天）原样放行，不碰。
var mitmHosts = regexp.MustCompile(`^(channels\.weixin\.qq\.com|res\.wx\.qq\.com|[a-z0-9.-]+\.video\.qq\.com)(:443)?$`)

const cacheTag = "sphdl2" // 改变脚本地址，避免微信用缓存里没改过的旧脚本

var (
	htmlScriptSrc = regexp.MustCompile(`src="([^"?]+)\.js"`)
	htmlCSPMeta   = regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']?content-security-policy[^>]*>`)

	jsFrom       = regexp.MustCompile(`from ?"([^"?]+)\.js"`)
	jsDep        = regexp.MustCompile(`"js/([^"?]+)\.js"`)
	jsLazyImport = regexp.MustCompile(`import\("([^"?]+)\.js"\)`)
	jsImport     = regexp.MustCompile(`import ?"([^"?]+)\.js"`)

	jsFlowTab       = regexp.MustCompile(`flowTab:([a-zA-Z_$][a-zA-Z0-9_$]*)`)
	jsLocalFlowTab  = regexp.MustCompile(`localFlowTab:([a-zA-Z_$][a-zA-Z0-9_$]*)`)
	jsGoNext        = regexp.MustCompile(`goToNextFlowFeed:([a-zA-Z_$][a-zA-Z0-9_$]*)`)
	jsGoPrev        = regexp.MustCompile(`goToPrevFlowFeed:([a-zA-Z_$][a-zA-Z0-9_$]*)`)
	jsLoadLocal     = regexp.MustCompile(`loadLocalPlaylist:([a-zA-Z_$][a-zA-Z0-9_$]*)`)
	jsPreload       = regexp.MustCompile(` ([a-zA-Z_$][a-zA-Z0-9_$]*\.invoke\([a-zA-Z_$][a-zA-Z0-9_$]*\));\(`)
	jsCommentDetail = regexp.MustCompile(`async finderGetCommentDetail\((\w+)\)\{(.*?)\}async`)
	jsPcFlow        = regexp.MustCompile(`async finderPcFlow\((\w+)\)\{(.*?)\}async`)
	jsRecommend     = regexp.MustCompile(`async finderGetRecommend\((\w+)\)\{(.*?)\}async`)
	// 页面所有接口（走微信 JSAPI，不走网络）的总出口：async post(t){try{return await this.invoke(t)}…
	// 不管首页加载更多用的是 finderPcFlow / finderStream / 以后改成什么名字，结果都从这里过。
	jsPost = regexp.MustCompile(`async post\(([a-zA-Z_$][a-zA-Z0-9_$]*)\)\{try\{return await this\.invoke\(([a-zA-Z_$][a-zA-Z0-9_$]*)\)\}`)
	// FlowFeedList 的 currentFeedIndex=0 字段：改成 getter/setter，谁改“当前第几条”都知道（划动、点击、连播、详情页切换，
	// goToNextFlowFeed 直接赋值、不走 setCurrentFeedIndex，所以只钩函数会漏）。
	jsCurIdxField   = regexp.MustCompile(`(class FlowFeedList extends [a-zA-Z_$][a-zA-Z0-9_$]*\{[^{}]*?)currentFeedIndex=0;`)
	jsUserPage      = regexp.MustCompile(`async finderUserPage\((\w+)\)\{(.*?)\}async`)
	jsRecommendTabs = regexp.MustCompile(`async getRecommendTabsFromService\(\)\{(.*?)\}(async\b|\}const\b)`)
)

// injectVersion 注入脚本的指纹。页面上的脚本和它不一致时，让页面自己刷新。
// patchRev 改了 patchFinderJS 的改写规则就改它，开着的页面会自动刷新换上新规则。
const patchRev = "2026-11-27-curidx2"

var injectVersion = func() string {
	h := sha1.Sum([]byte(injectJS + patchRev))
	return hex.EncodeToString(h[:4])
}()

func esc(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// wrapAsync 把页面里的接口函数包一层：原逻辑不变，拿到结果后顺手交给 __sphdl。
func wrapAsync(re *regexp.Regexp, js, name, kind string) (string, bool) {
	if !re.MatchString(js) {
		return js, false
	}
	tpl := `async ` + name + `($1){var __r=await (async()=>{$2;})();try{window.__sphdl&&window.__sphdl("` + kind + `",__r,"` + name + `")}catch(e){}return __r}async`
	return re.ReplaceAllString(js, tpl), true
}

func flowFeedHook(js string, re *regexp.Regexp, method, varName string) (string, bool) {
	if !re.MatchString(js) || varName == "" {
		return js, false
	}
	v := esc(varName)
	tpl := method + `:async function(...a){var __r=await $1(...a);try{var s=` + v + `&&` + v + `.value;if(s&&s.feeds)window.__sphdl&&window.__sphdl("current",s.feeds[s.currentFeedIndex],"` + method + `");else window.__sphdl&&window.__sphdl("current",null,"` + method + `:没有feeds")}catch(e){}return __r}`
	return re.ReplaceAllString(js, tpl), true
}

// patchFinderJS 改写视频号前端脚本，返回改写后的内容和命中了哪些钩子（写进日志方便排查）。
func patchFinderJS(path, js string) (string, []string) {
	var hits []string
	add := func(name string, ok bool) {
		if ok {
			hits = append(hits, name)
		}
	}
	tag := `.js?t=` + cacheTag
	js = jsFrom.ReplaceAllString(js, `from"$1`+tag+`"`)
	js = jsDep.ReplaceAllString(js, `"js/$1`+tag+`"`)
	js = jsLazyImport.ReplaceAllString(js, `import("$1`+tag+`")`)
	js = jsImport.ReplaceAllString(js, `import"$1`+tag+`"`)

	flowVar := ""
	if m := jsFlowTab.FindStringSubmatch(js); m != nil {
		flowVar = m[1]
	}
	var ok bool
	if strings.Contains(path, "virtual_svg-icons-register") {
		if jsPreload.MatchString(js) {
			js = jsPreload.ReplaceAllString(js, ` (async()=>{const __r=await $1;try{window.__sphdl&&window.__sphdl("list",__r,"preload")}catch(e){}return __r})();(`)
			add("preload", true)
		}
		js, ok = wrapAsync(jsCommentDetail, js, "finderGetCommentDetail", "current")
		add("commentDetail", ok)
		js, ok = wrapAsync(jsPcFlow, js, "finderPcFlow", "list")
		add("pcFlow", ok)
		js, ok = wrapAsync(jsRecommend, js, "finderGetRecommend", "list")
		add("recommend", ok)
		js, ok = wrapAsync(jsUserPage, js, "finderUserPage", "list")
		add("userPage", ok)
		if jsPost.MatchString(js) {
			js = jsPost.ReplaceAllString(js, `async post($1){try{var __r=await this.invoke($2);try{window.__sphdl&&__r&&__r.data&&window.__sphdl("list",__r.data,"post:"+($1&&$1.name||""))}catch(e){}return __r}`)
			add("post", true)
		}
		if jsCurIdxField.MatchString(js) {
			js = jsCurIdxField.ReplaceAllString(js, `${1}__sphCi=0;get currentFeedIndex(){return this.__sphCi}set currentFeedIndex(v){var o=this.__sphCi;this.__sphCi=v;if(v!==o){var self=this;setTimeout(function(){try{var f=self.currentFeed;f&&f.id&&f.id!=="0"&&window.__sphdl&&window.__sphdl("current",f,"currentFeedIndex")}catch(e){}},0)}}`)
			add("curIdx", true)
		}
		if jsRecommendTabs.MatchString(js) {
			js = jsRecommendTabs.ReplaceAllString(js, `async getRecommendTabsFromService(){var __r=await (async()=>{$1;})();try{window.__sphdl&&window.__sphdl("list",__r,"recommendTabs")}catch(e){}return __r}${2}`)
			add("recommendTabs", true)
		}
	}
	if strings.Contains(path, "virtual_svg-icons-register") || strings.Contains(path, "connect.publish") || strings.Contains(path, "applyMic.publish") {
		js, ok = flowFeedHook(js, jsGoNext, "goToNextFlowFeed", flowVar)
		add("goNext", ok)
		js, ok = flowFeedHook(js, jsGoPrev, "goToPrevFlowFeed", flowVar)
		add("goPrev", ok)
		if m := jsLocalFlowTab.FindStringSubmatch(js); m != nil {
			js, ok = flowFeedHook(js, jsLoadLocal, "loadLocalPlaylist", m[1])
			add("localPlaylist", ok)
		}
	}
	return js, hits
}

func injectHTML(html string) string {
	html = htmlCSPMeta.ReplaceAllString(html, "")
	html = htmlScriptSrc.ReplaceAllString(html, `src="$1.js?t=`+cacheTag+`"`)
	script := "<script>" + strings.Replace(injectJS, "__SPHDL_V__", injectVersion, 1) + "</script>"
	if i := strings.Index(strings.ToLower(html), "<head>"); i >= 0 {
		return html[:i+6] + script + html[i+6:]
	}
	return script + html
}

func replaceBody(resp *http.Response, body string) {
	resp.Body = io.NopCloser(strings.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Security-Policy")
	resp.Header.Del("Content-Security-Policy-Report-Only")
	resp.Header.Del("ETag")
	resp.Header.Set("Cache-Control", "no-store")
}

func readBody(resp *http.Response) (string, bool) {
	if resp.Body == nil {
		return "", false
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(b))
		return "", false
	}
	return string(b), true
}

func fromChannels(r *http.Request) bool {
	for _, h := range []string{r.Header.Get("Referer"), r.Header.Get("Origin")} {
		if strings.Contains(h, "channels.weixin.qq.com") {
			return true
		}
	}
	return false
}

func newProxy(ca tls.Certificate, store *Store, upstream string) *goproxy.ProxyHttpServer {
	p := goproxy.NewProxyHttpServer()
	p.Logger = log.New(io.Discard, "", 0)
	if upstream != "" {
		if u, err := url.Parse(upstream); err == nil {
			p.Tr.Proxy = http.ProxyURL(u)
			p.ConnectDial = p.NewConnectDialToProxy(upstream)
		}
	} else {
		p.Tr.Proxy = nil
		p.ConnectDial = nil
	}

	mitm := &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: goproxy.TLSConfigFromCA(&ca)}
	p.OnRequest(goproxy.ReqHostMatches(mitmHosts)).HandleConnectFunc(
		func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			tracef("CONNECT %s (解密) UA=%s", host, ctx.Req.Header.Get("User-Agent"))
			return mitm, host
		})
	p.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if strings.Contains(host, "qq.com") || strings.Contains(host, "weixin") {
			tracef("CONNECT %s (放行)", host)
		}
		return goproxy.OkConnect, host
	})

	p.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		host := r.URL.Hostname()
		if host == "channels.weixin.qq.com" && strings.HasPrefix(r.URL.Path, "/__sphdl/") {
			return r, handleReport(r, store)
		}
		if strings.HasSuffix(host, ".video.qq.com") {
			if k := r.URL.Query().Get("encfilekey"); k != "" {
				store.OnMediaRequest(k)
			}
		}
		if host == "res.wx.qq.com" && fromChannels(r) {
			// 不让微信拿 304 缓存，否则改不到脚本
			r.Header.Del("If-None-Match")
			r.Header.Del("If-Modified-Since")
		}
		return r, nil
	})

	p.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if resp == nil || ctx.Req == nil {
			return resp
		}
		host := ctx.Req.URL.Hostname()
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if strings.HasSuffix(host, ".video.qq.com") && strings.Contains(ctx.Req.URL.Path, "stodownload") && strings.Contains(ct, "video") {
			store.OnWebMedia(ctx.Req.URL.Query().Get("encfilekey"), rangeStart(resp.Header))
		}
		if strings.HasSuffix(host, ".video.qq.com") && strings.Contains(ctx.Req.URL.Path, "stodownload") {
			if ek, want := store.OnLiteMedia(ctx.Req.URL.String(), ct, resp.Header, resp.StatusCode); want && resp.Body != nil && startsAtZero(resp.StatusCode, resp.Header) {
				resp.Body = &headTap{r: resp.Body, fn: func(b []byte) { store.SetLiteHead(ek, b) }}
			}
		}
		if !strings.Contains(ct, "video") && !strings.Contains(ct, "image") {
			tracef("%d %s %s%s ct=%s ref=%s origin=%s", resp.StatusCode, ctx.Req.Method, host, ctx.Req.URL.Path, ct,
				ctx.Req.Header.Get("Referer"), ctx.Req.Header.Get("Origin"))
		}
		if debugDump && (host == "res.wx.qq.com" && strings.Contains(ct, "javascript") || host == "channels.weixin.qq.com" && strings.Contains(ct, "html")) {
			if body, ok := readBody(resp); ok {
				dumpFile(host+ctx.Req.URL.Path, body)
				resp.Body = io.NopCloser(strings.NewReader(body))
				resp.Header.Del("Content-Encoding")
			}
		}
		switch {
		case host == "channels.weixin.qq.com" && strings.Contains(ct, "text/html"):
			if body, ok := readBody(resp); ok {
				replaceBody(resp, injectHTML(body))
				logf("已注入视频号页面 %s", ctx.Req.URL.Path)
			}
		case host == "res.wx.qq.com" && strings.Contains(ct, "javascript") && fromChannels(ctx.Req) &&
			!strings.Contains(ctx.Req.URL.Path, "wasm_video_decode"):
			if body, ok := readBody(resp); ok {
				js, hits := patchFinderJS(ctx.Req.URL.Path, body)
				replaceBody(resp, js)
				if len(hits) > 0 {
					logf("已改写脚本 %s：%s", lastSeg(ctx.Req.URL.Path), strings.Join(hits, ","))
				}
			}
		}
		return resp
	})
	return p
}

// debugBuild 打包时用 -ldflags "-X main.debugBuild=1" 打开调试记录
var debugBuild string

var (
	debugDump = os.Getenv("SPHDL_DEBUG") == "1" || debugBuild == "1"
	traceMu   sync.Mutex
)

// tracef 把经过的视频号相关请求记到 debug/trace.log，排查问题用。
func tracef(format string, a ...any) {
	if dataDir == "" {
		return
	}
	traceMu.Lock()
	defer traceMu.Unlock()
	dir := filepath.Join(dataDir, "debug")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "trace.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5<<20 { // 超过 5MB 就换新文件
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05 ")+format+"\n", a...)
}

func dumpFile(name, body string) {
	safe := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(name, "_")
	if len(safe) > 150 {
		safe = safe[len(safe)-150:]
	}
	os.MkdirAll(filepath.Join(dataDir, "debug", "files"), 0o755)
	os.WriteFile(filepath.Join(dataDir, "debug", "files", safe), []byte(body), 0o644)
}

func lastSeg(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

type reportPayload struct {
	Kind  string     `json:"kind"`
	Items []FeedInfo `json:"items"`
	Href  string     `json:"href"`
	Msg   string     `json:"msg"`
	UA    string     `json:"ua"`
	Via   string     `json:"via"` // 哪个钩子报上来的（排查用）
	ID    string     `json:"id"`  // kind=playing：正在播的视频编号
}

func handleReport(r *http.Request, store *Store) *http.Response {
	if r.URL.Path == "/__sphdl/task" {
		if v := r.URL.Query().Get("v"); v != injectVersion {
			logf("视频号页面的脚本是旧的，让它自动刷新")
			return goproxy.NewResponse(r, "application/json", http.StatusOK, `{"reload":true}`)
		}
		u := store.NextTask()
		b, _ := json.Marshal(map[string]any{"url": u, "inPlace": store.OpenInPlace(), "resend": store.TakeResend()})
		if u != "" {
			logf("让微信打开视频：%s", trimURL(u))
		}
		return goproxy.NewResponse(r, "application/json", http.StatusOK, string(b))
	}
	var p reportPayload
	b, _ := io.ReadAll(io.LimitReader(r.Body, 20<<20))
	err := json.Unmarshal(b, &p)
	if err != nil {
		logf("页面的上报格式不对，没收下：%v（%s）", err, firstN(string(b), 200))
	}
	if err == nil {
		switch p.Kind {
		case "hello":
			logf("视频号页面已连上：%s", trimURL(p.Href))
			store.MarkPageSeen()
		case "error":
			logf("页面脚本出错：%s", p.Msg)
		case "log":
			logf("页面：%s", p.Msg)
		case "playing":
			if !store.OnPlaying(p.ID, p.Msg) {
				tracef("播放地址：%s", trimURL(p.Msg))
			}
		default:
			first := ""
			if p.Kind == "current" && len(p.Items) > 0 {
				first = "｜" + p.Items[0].Nickname + "：" + p.Items[0].Title
				if r := []rune(first); len(r) > 30 {
					first = string(r[:30])
				}
			}
			tracef("report kind=%s n=%d via=%s%s href=%s", p.Kind, len(p.Items), p.Via, first, trimURL(p.Href))
			store.AddReport(p.Kind, p.Items, p.Href)
		}
	}
	resp := goproxy.NewResponse(r, "application/json", http.StatusOK, `{"ok":true}`)
	resp.Header.Set("Cache-Control", "no-store")
	return resp
}

func trimURL(s string) string {
	if i := strings.Index(s, "?"); i >= 0 {
		return s[:i]
	}
	return s
}

func firstN(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
