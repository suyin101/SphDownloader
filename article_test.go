package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseArticles(t *testing.T) {
	refs := parseArticles(`看这个 https://mp.weixin.qq.com/s/4BtL3cX8fZelnheMA7wAew 还有
https://mp.weixin.qq.com/s?__biz=MzU4NTAxNTQ3OA%3D%3D&amp;mid=2247492994&amp;idx=1&amp;sn=453b&amp;chksm=fd93&scene=21
https://mp.weixin.qq.com/s/4BtL3cX8fZelnheMA7wAew 重复的 https://weixin.qq.com/sph/AuQGcNgaw`)
	if len(refs) != 2 {
		t.Fatalf("应该 2 条，得到 %+v", refs)
	}
	if refs[0].Key != "mp:4BtL3cX8fZelnheMA7wAew" || refs[1].Key != "mp:MzU4NTAxNTQ3OA==-2247492994-1" {
		t.Fatalf("key 不对：%+v", refs)
	}
	if strings.Contains(refs[1].URL, "chksm") || !strings.Contains(refs[1].URL, "sn=453b") {
		t.Fatalf("长链接没整理好：%s", refs[1].URL)
	}
}

func TestParseGalleryPage(t *testing.T) {
	page := `<meta property="og:title" content="如何选鱼油？"><meta property="og:image" content="http://mmbiz.qpic.cn/c/0?wx_fmt=jpeg">
nick_name: 'Poincre', create_time: '1787932613',
picture_page_info_list: [
  { cdn_url: 'https://mmbiz.qpic.cn/a1/0?wx_fmt=png', width: '941' * 1, title: 'x{[', live_photo: { format_info: [ ], },
    watermark_info: { cdn_url: 'http://mmbiz.qpic.cn/w1/0?wx_fmt=png', is_uploader: true, },
    share_cover: { file_id: '0' * 1, cdn_url: '', }, },
  { cdn_url: 'http://mmbiz.qpic.cn/a2/0?wx_fmt=png', width: '941' * 1,
    watermark_info: { cdn_url: 'http://mmbiz.qpic.cn/w2/0?wx_fmt=png', }, },
],
picture_page_info_list = ((window.cgiDataNew && window.cgiDataNew.picture_page_info_list) || []);
<div id="js_content"><img data-src="https://mmbiz.qpic.cn/zz/640?wx_fmt=jpeg"></div>`
	info := parseArticlePage(page)
	want := []string{"https://mmbiz.qpic.cn/a1/0?wx_fmt=png", "https://mmbiz.qpic.cn/a2/0?wx_fmt=png"}
	if !info.Gallery || strings.Join(info.Images, ",") != strings.Join(want, ",") {
		t.Fatalf("图片不对：%+v", info.Images)
	}
	if info.Title != "如何选鱼油？" || info.Nickname != "Poincre" || info.CreateTime != 1787932613 {
		t.Fatalf("信息不对：%+v", info)
	}
}

func TestParseNormalArticle(t *testing.T) {
	page := `<meta property="og:title" content="海豹油&amp;鱼油">var nickname = htmlDecode("科学辟谣");var ct = "1746181800";
picture_page_info_list: [],
<div id="js_content"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/a/640?wx_fmt=jpeg&amp;from=appmsg">
<img data-src="https://mmbiz.qpic.cn/sz_mmbiz_gif/b/640?wx_fmt=gif"></div><script>var x='<img data-src="https://mmbiz.qpic.cn/ad/0">'</script>`
	info := parseArticlePage(page)
	if info.Gallery || len(info.Images) != 2 || info.Images[0] != "https://mmbiz.qpic.cn/sz_mmbiz_jpg/a/0?wx_fmt=jpeg&from=appmsg" {
		t.Fatalf("普通文章图片不对：%+v", info.Images)
	}
	if info.Title != "海豹油&鱼油" || info.Nickname != "科学辟谣" || info.CreateTime != 1746181800 {
		t.Fatalf("信息不对：%+v", info)
	}
}

// LIVE=1：真实下载用户给的小绿书，检查 10 张都是原图（PNG 无水印版）
func TestLiveArticle(t *testing.T) {
	if os.Getenv("LIVE") == "" {
		t.Skip("LIVE=1 才跑")
	}
	dir := t.TempDir()
	s := NewStore(dir)
	s.AutoDownload = true
	if n := s.AddLinks("https://mp.weixin.qq.com/s/4BtL3cX8fZelnheMA7wAew"); n != 1 {
		t.Fatalf("添加数 %d", n)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		it, _ := s.Get("mp:4BtL3cX8fZelnheMA7wAew")
		if it.Status == "error" {
			t.Fatal(it.Err)
		}
		if it.Status == "done" {
			files, _ := filepath.Glob(filepath.Join(it.File, "*"))
			t.Logf("%s：%d 个文件 %v", it.File, len(files), files)
			if len(files) != 10 || it.Images != 10 {
				t.Fatalf("应该 10 张")
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("超时")
}
