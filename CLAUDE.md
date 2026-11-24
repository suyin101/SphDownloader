# SphDownloader 视频号下载器（Mac + Windows）

公开仓库 https://github.com/suyin101/SphDownloader 。发新版：改 main.go 的 version → `./release.sh --publish "更新说明"`
（打包 Windows exe/zip + Mac zip 并 gh release create；软件启动时检查 releases/latest，开着自动更新就自动装）。
updater.go + update_windows.go（运行中的 exe 改名 .old 再换新）/ update_darwin.go（换 .app 后 open -n）。
Windows 证书优先装“本地计算机”（UAC 提权），失败退到当前用户；图标资源由 assets/make_syso.py 生成（不依赖外部工具）。

原理：本机 HTTPS 代理（goproxy），只解密 channels.weixin.qq.com / res.wx.qq.com / *.video.qq.com，其它网站直接放行（原来有代理会当上游）。
- proxy.go：给视频号页面注入 inject.js；改写 res.wx.qq.com 的 virtual_svg-icons-register / connect.publish 脚本，
  finderGetCommentDetail、goToNext/PrevFlowFeed、loadLocalPlaylist 上报 "current"（自动下载），列表类接口上报 "list"。
  正则参考 ltaoo/wx_channels_download 的 pkg/scraper/wxchannels/interceptor.go，微信改版后去对照它更新。
  2026-11-13 在 Mac 微信 4.1.15 实测 9 个钩子全命中，下载解密成功。
- 【灰度坑】同为 Windows 最新版微信，部分账号/电脑点分享卡片时，预览页 /finder-preview/pages/sph 调 openUrlWithExtraWebview(type:FinderFeed)
  会被微信改走自带播放器 liteapp（日志只见 liteapp.weixin.qq.com、没有 /web/pages/feed），所有同类工具都只抓到图片。
  v1.4.2 起 inject.js 在预览页拦截这个调用，改用 location.replace 在当前网页打开视频页（2026-11-13 对方电脑实测成功）。
  链接下载同理：v1.4.3 起先用新窗口打开，10 秒没打开视频网页就记下 store.inPlace，之后改为 location.href 在当前网页跳转（task 返回 inPlace）。
- 【自带播放器模式 lite.go，v1.5.0，只在 Windows】微信 4.2 起按账号灰度，左侧“视频号”整个走原生 liteapp：没网页可注入，decodeKey 不走网络
  （2026-11-19 起用户 Windows 就是这样，一直“未连接”）。做法参考 laoyu20032003/wx_video_downloader：代理在 OnResponse 里把 *.video.qq.com/stodownload
  视频响应交给 store.OnLiteMedia（网页模式 30 秒内在线就不管）→ liteLoop 取密文前 16 字节 → memscan_windows.go 读 WeChatAppEx.exe（隔次加 Weixin.exe）
  内存，extractSeeds 宽松收集候选（"decodeKey":"…"、kvReport 串 oid,URL,key,、encfilekey 附近所有数字，UTF-8 和 UTF-16 都搜）→ seedMatches 用“解密后是 ftyp”精确验证。
  正在看（地址带 m= 或请求≥3 次）还找不到钥匙时，退路：内存里找已解密的 ftyp+moov 开头，headerFits 顶层 box 正好接到文件末尾才认，钥匙流=密文开头 XOR 明文开头（Item.ks）。
  条目 Key = lite:<钥匙>，标题只在窗口里同时有这条 encfilekey 时才取 description，否则“视频号视频 时间”。下载用播放器拉的完整地址（itemURLs）。
  测试 lite_test.go 用假内存+假视频服务器。【v1.5.1 2026-11-20 用户 Windows 实测下载成功】v1.5.0 的坑：逐个去服务器读开头会卡住，现在由代理转发时 headTap 截开头。
- inject.js：深度搜索 objectDesc.media，POST 同源 /__sphdl/report；每 2 秒 GET /__sphdl/task 取“链接下载”任务并让微信打开。
- link.go：weixin.qq.com/sph/xxx → 公开接口 finder-preview/api/feed/get_feed_info（免登录）拿标题/作者/封面/dynamicExportId，
  拼 /web/pages/feed?eid=…，交给页面用 WeixinJSBridge openUrlWithExtraWebview 打开（失败就 location.href），
  页面上报 current 时按 href 里的 eid 对上任务，强制下载。一次只开一个，25 秒超时重试 2 次。【链接下载 2026-11-13 实测成功】
- 【原画已封】2026-11-19 调研：地址里 basedata 是签过名的可下载档位名单（只有 xWT111/112/113/126/127/128 + 音频 xA0/xA2），原片不在名单里，改参数无效。
  ltaoo #562/#581、nobiyou #98、qiye45 #178、putyy #416 都确认 2026-08 底起原画下不了，最高就是微信给的转码档（常见 720，部分视频有 1080）。微信再放开时再看。
- article.go（v1.6.0）：公众号文章链接 mp.weixin.qq.com/s/xxx 或 s?__biz=… 直接下图片，不用微信、不用嗅探。小绿书（item_show_type=8）取 picture_page_info_list
  每个对象里第一个 cdn_url（原图，无水印；watermark_info.cdn_url 是带「公众号 · 名字」水印的）；普通文章取 #js_content 的 data-src，/640? 换 /0? 原尺寸。
  存成「作者_标题_日期/01.png…」文件夹，条目 Key=mp:…，Source=article。2026-11-24 实测小绿书 10 张、普通文章 7 张都成功。
- history.go：下载记录 history.json，下过的视频/链接不再自动重下（手动“重新下载”除外）。sniffer.go：嗅探开关，关=还原代理+关端口+断开连接。
- store.go：下载队列（3 并发）。【清晰度】只留 encfilekey/token 的“原画质”地址实测只有 1024×576/350kbps；v1.4.4 起 inject.js 上报 media.spec 档位，
  candidateURLs 先用完整地址 + &X-snsvideoflag=最高档（levelOrder 最小的正数，同档比码率，参照 ltaoo PickHighestSpec），失败再退回旧地址。封面，/api/preview 边下边解密（支持 Range）。
- 窗口：Mac webview_go（cgo），Windows go-webview2（纯 Go）。关窗口=退出并还原代理；guard.go 守护进程兜底还原。
- sys_darwin.go：networksetup 改所有网络服务的 web/secure 代理、关 SOCKS，退出恢复；security add-trusted-cert 进登录钥匙串。
- sys_windows.go：HKCU 注册表代理；certutil -user 装证书。

测试：`SAMPLE_MP4=任意大于128KB的mp4 LIVE=1 go test -race ./...`；界面截图：`DEMO=1 COVER=x.jpg go test -run TestUIDemo` 后访问 127.0.0.1:18899。
打包：`DEBUG=1 ./build-mac.sh`（Mac .app，DEBUG 记录 ~/Library/Application Support/SphDownloader/debug/）；`./release.sh`（Windows zip）。
auto mode 下 Claude 不能自己启动软件（会改系统代理/装证书），要用户双击。
