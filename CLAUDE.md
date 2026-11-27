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
- 【转文字 v1.7.0】transcribe.go + mp4audio.go：给编导看文案用，本机识别、不上传。列表“转文字”/批量“转文字”（没下载的先下载再转），设置“下载完自动转文字”（默认关）。
  结果只存和视频同名的 .txt（标题、作者/发布/时长一行，正文按停顿>1.2 秒或满 160 字分段，带 BOM，Windows 用 CRLF）；视频旁有同名 .txt 就算转过（syncTxLocked）。
  声音：mp4audio.go 纯 Go 拆 mp4 + skrashevich/go-aac 解 AAC-LC；HE-AAC/HE-AACv2 改写 ASC 只解基础层（coreASC，采样率减半、单声道，识别不受影响）。不用 ffmpeg。
  识别：sherpa-onnx v2.0.3 命令行 sherpa-onnx-vad-with-offline-asr（改名 sherpa-asr）+ silero_vad + SenseVoice int8 **2024-07-17**（2025-09-09 版没有标点，别换）。
  调用时 cmd.Dir=组件目录、参数全用相对路径（Windows 用户名有中文时命令行程序读不了中文路径）；Windows 用 static-MT 版单个 exe，不依赖 VC++ 运行库。
  组件在 dataDir/asr，第一次用时下载（约 248MB，断点续传 + sha256 校验）：程序包 asr-runtime-<平台>.zip 只在 GitHub 发布 asr-v1（--latest=false！）；
  模型 model.int8.onnx/tokens.txt 先 hf-mirror.com，再 huggingface.co，再 asr-v1。asr-build/（gitignore）里有原始下载和 pack/ 打包结果，release.sh 首次发版时自动建 asr-v1。
  速度（M 系列 Mac）：74 秒视频 2.6 秒，12 分钟视频 4 秒。测试：`ASR_DIR=装好组件的目录 SAMPLE_MP4=x.mp4 go test -run TestTranscribeLive -v`；
  `AUDIO_DIR=一批mp4 go test -run 'TestDecodeMP4Audio|TestDecodeVsFFmpeg' -v` 检查解声音（和 ffmpeg 比相关度，要 ≥0.9）。
  【坑 v1.7.0】go-aac v0.1.0 的 PNS 有 bug，解出来只剩约三成声音，长视频后半段识别成乱码；v1.7.1 升到 800d9fa 修好（相关 0.99）。HE-AACv2 音乐和 ffmpeg 对不上属正常（PS 合成立体声），人声样本 0.99。
  v1.7.1：转文字进度（识别程序边识别边输出，按“识别到第几秒/总时长”算，TxPct）；“转文字”按钮只在勾选后显示；文案窗口可“重新转文字”。
- 【识别模式 v1.9.0】设置里“精准/快速”，默认精准（config asrMode）。2026-11-27 用三段视频对比 4 个模型（asr-build/cmp/）：
  Qwen3-ASR 0.6B int8 最准（核心/强点/威胁/窗口期/硬仗/马化腾/大厂都对），FunASR-Nano 次之，SenseVoice 同音字和人名错得多，FireRedASR2 没标点。
  精准 = Qwen3（csukuangfj2/sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25，hf-mirror 能下，约 985MB，放 asr/qwen3/），Mac 74 秒视频约 10 秒、12 分钟约 1.5 分钟；快速 = SenseVoice。
  热词（--qwen3-asr-hotwords 用标题词）实测效果不稳定，没用。别加 --silero-vad-max-speech-duration=15（切断句子多错字）。
  Qwen3 输出汉字数字，textnorm.go normalizeNumbers 只在后面跟单位时转阿拉伯数字（成语/一下子/千万别/三个人不动），textnorm_test.go 有例句。
  release.sh 发版时把 Qwen3 文件也传到 asr-v1 当备用源（tokenizer/ 下的文件改名 tokenizer_xxx）。AI 校对（大模型改错字）用户说先不做。
- 【列表实时 v1.9.1】用户 Windows 反映列表有延迟。三个原因：①界面每秒轮询 → 改成 /api/events 推送（服务端每 200ms 比 stateSig 指纹，变了就推，实测 0.1 秒）+ 2 秒兜底轮询 + 切回窗口立即刷新；
  ②窗口在后台被系统降速 → Windows 设 WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS 关后台节流，Mac 用 NSProcessInfo beginActivity 关 App Nap；
  ③自带播放器模式要扫内存找到钥匙才出条目 → 开始播放就先放占位条目 lite-wait:<ek>（“正在找解密钥匙”），找到后替换（保持位置、正在看），找不到改成失败说明。
- 【缩略图 v1.7.1】封面经 /api/coverimg 由软件代取（Windows 网页控件直接加载微信图片可能失败）；自带播放器模式没有封面，界面用 /api/preview 截视频第 1 秒一帧（pumpFrames，一次一个）。
  界面截图用 Electron（headless Chrome 不支持 H.264）：AutoCut 的 node_modules/.bin/electron 离屏打开 DEMO 页面 capturePage。
- 【小红书 v1.8.0】xhs.go：识别 xhslink.cn/o/…、xiaohongshu.com/discovery/item|explore/<24位id>（整段分享文字直接贴），手机 UA 打开分享页读 window.__INITIAL_STATE__（undefined 要换成 null），
  findNote 找有 noteId+imageList 的对象。图片原图 = sns-img-bd.xhscdn.com/<fileId>（无水印，原上传尺寸）；视频取 media.stream.h264 最大一档，masterUrl 带 sign 参数，
  backupUrls（sns-bak-*）也能下，备用排前面（Item.FeedInfo.alt）。视频笔记 Source=xhs 走普通下载/预览/转文字；图文笔记 isPics() 走 downloadArticle 存图片文件夹 + 正文.txt。
  store.addWebLocked 统一处理公众号和小红书（parseWebRefs/fetchWeb），短链接 Key=xhs:s:<短码>，解析后 ID=xhs:<笔记id>。2026-11-26 用户给的两条链接实测下载成功。
- 【抖音 v1.8.0，做法 B】douyin.go + douyin_hook.js + douyin_darwin.m/.go（隐藏 NSWindow+WKWebView，默认数据存储记住游客身份）/ douyin_windows.go（单独线程的 WebView2，DataPath=webview2-douyin，ShowWindow 隐藏）。
  短链 v.douyin.com 只用 HTTP 取跳转里的作品 ID，然后隐藏网页打开 www.douyin.com/video/<id>，hook 旁听网页自己请求的 /aweme/v1/web/aweme/detail/（签名、Cookie 都是抖音网页自己做的），
  取 bit_rate 里最清晰的 h264 play_addr（无水印），图文取 images[].url_list（download_url_list 带水印）。下载要带 Referer（setReferer）。同一时间只开一个隐藏网页（douyinBusy）。
  【坑】抖音每次打开都有后台“无感验证”（隐形 verifycenter iframe），别当成验证码；只认显示出来、够大的验证框，连续 3 次（约 6 秒）才弹窗让用户手动过。
  2026-11-26 Mac 实测：不登录 5～10 秒拿到 1080×1440 无水印视频，转文字正常。Windows 未实测，抖音图文未实测（没有样例链接）。
  测试：`DY_LIVE="分享文字" go test -count=1 -run TestDouyinLive -v`（Mac，TestMain 在主线程跑 Cocoa 事件循环）；注意 go test 会缓存结果，要 -count=1。
  做法 A（a_bogus 签名直接调接口）：Evil0ctal/Douyin_TikTok_Download_API v5（Apache-2.0）有纯 Python 实现，但 Claude Code 自动安全检查会拦，用户说以后再研究。
- history.go：下载记录 history.json，下过的视频/链接不再自动重下（手动“重新下载”除外）。sniffer.go：嗅探开关，关=还原代理+关端口+断开连接。
- store.go：下载队列（3 并发）。【清晰度】只留 encfilekey/token 的“原画质”地址实测只有 1024×576/350kbps；v1.4.4 起 inject.js 上报 media.spec 档位，
  candidateURLs 先用完整地址 + &X-snsvideoflag=最高档（levelOrder 最小的正数，同档比码率，参照 ltaoo PickHighestSpec），失败再退回旧地址。封面，/api/preview 边下边解密（支持 Range）。
- 窗口：Mac webview_go（cgo），Windows go-webview2（纯 Go）。关窗口=退出并还原代理；guard.go 守护进程兜底还原。
- sys_darwin.go：networksetup 改所有网络服务的 web/secure 代理、关 SOCKS，退出恢复；security add-trusted-cert 进登录钥匙串。
- sys_windows.go：HKCU 注册表代理；certutil -user 装证书。

测试：`SAMPLE_MP4=任意大于128KB的mp4 LIVE=1 go test -race ./...`；界面截图：`DEMO=1 COVER=x.jpg go test -run TestUIDemo` 后访问 127.0.0.1:18899。
打包：`DEBUG=1 ./build-mac.sh`（Mac .app，DEBUG 记录 ~/Library/Application Support/SphDownloader/debug/）；`./release.sh`（Windows zip）。
auto mode 下 Claude 不能自己启动软件（会改系统代理/装证书），要用户双击。
