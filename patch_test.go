package main

import (
	"os"
	"strings"
	"testing"
)

const fakeFinderJS = `import{a as b}from"./index.publish.js";const lazy=()=>import("./chunk.publish.js");var bridge={invoke:async e=>({data:{object:{id:"111",objectDesc:{mediaType:4,description:"测试视频 #话题",media:[{url:"https://finder.video.qq.com/251/20302/stodownload?encfilekey=AAA&token=TTT",urlToken:"&a=1",decodeKey:"987654321",coverUrl:"c"}]},contact:{nickname:"作者"}}}})};var yt={value:{feeds:[{id:"222",objectDesc:{media:[{url:"https://x.video.qq.com/s?encfilekey=B&token=T",decodeKey:"5"}]}}],currentFeedIndex:0}};async function nx(v){return "nx"+v}class W{async finderInit(){return 1}async finderGetCommentDetail(e){const t=await bridge.invoke(e);return t}async finderPcFlow(e){const t=await bridge.invoke(e);return t}async other(){return 2}}const api={flowTab:yt,goToNextFlowFeed:nx};globalThis.W=W;globalThis.api=api;`

func TestPatchFinderJS(t *testing.T) {
	out, hits := patchFinderJS("/t/wx_fed/finder/web/web-finder/res/js/virtual_svg-icons-register.publish.abc.js", fakeFinderJS)
	want := []string{"commentDetail", "pcFlow", "goNext"}
	for _, w := range want {
		if !strings.Contains(strings.Join(hits, ","), w) {
			t.Fatalf("missing hook %s, hits=%v", w, hits)
		}
	}
	if !strings.Contains(out, `from"./index.publish.js?t=`+cacheTag+`"`) || !strings.Contains(out, `import("./chunk.publish.js?t=`) {
		t.Fatal("imports not rewritten")
	}
	if p := os.Getenv("PATCH_OUT"); p != "" {
		// 去掉 import 语句方便 node 直接跑
		os.WriteFile(p, []byte(out[strings.Index(out, ";")+1:]), 0o644)
	}
}

func TestInjectHTML(t *testing.T) {
	h := injectHTML(`<html><head><meta http-equiv="Content-Security-Policy" content="x"><script src="https://res.wx.qq.com/a/b.js"></script></head></html>`)
	if strings.Contains(h, "Content-Security-Policy") || !strings.Contains(h, `b.js?t=`+cacheTag) || !strings.Contains(h, "window.__sphdl") {
		t.Fatal(h)
	}
}

func TestFileName(t *testing.T) {
	n := fileName(FeedInfo{Title: "好看的/视频:标题? #话题 #另一个", Nickname: "作者*名", CreateTime: 1700000000})
	if strings.ContainsAny(n, `/\:*?"<>|`) || !strings.HasSuffix(n, ".mp4") || strings.Contains(n, "#") {
		t.Fatal(n)
	}
	t.Log(n)
}

func TestInjectVersionStamped(t *testing.T) {
	h := injectHTML("<html><head></head></html>")
	if strings.Contains(h, "__SPHDL_V__") || !strings.Contains(h, `"`+injectVersion+`"`) {
		t.Fatal("version not stamped")
	}
}
