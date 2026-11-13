# 视频号下载器

<img src="assets/icon-1024.png" width="96" align="right">

下载微信视频号视频的桌面软件，支持 **Windows** 和 **Mac**。

- 在电脑版微信里看视频号，看过的视频自动出现在列表里，勾选就能下载（原画质 MP4，自动解密）
- 粘贴视频号链接（`https://weixin.qq.com/sph/xxxx`）批量下载
- 软件里直接预览视频、下载封面图
- 下载过的视频不会重复下载
- 搜索、批量下载、清空列表
- 嗅探开关：关闭后网络设置还原，不影响上网
- 自动检查更新

## 下载

| 系统 | 下载 |
| --- | --- |
| Windows 10 / 11 | [SphDownloader-windows.zip](https://github.com/suyin101/SphDownloader/releases/latest/download/SphDownloader-windows.zip) |
| macOS 11+（Apple 芯片 / Intel） | [SphDownloader-mac.zip](https://github.com/suyin101/SphDownloader/releases/latest/download/SphDownloader-mac.zip) |

## 使用

1. 打开软件。第一次会提示安装一个本机证书：
   - Windows：点“确定”，再在“用户账户控制”里点 **是**
   - Mac：点“好”，输入开机密码并点 **更新设置**
2. 打开电脑版微信 → 左侧 **视频号**（已经开着的话，关掉视频号窗口再打开一次）
3. 软件左上角显示 **视频号已连接** 后：
   - 在微信里看视频，视频会出现在列表里，勾选后点 **下载选中**
   - 或者把视频号链接粘贴到上方输入框，点 **解析并下载**（下载期间视频号窗口保持打开）

> Mac 第一次打开如果提示“无法验证开发者”：在“应用程序”里右键软件 → 打开 → 打开。

万一网络异常（比如电脑突然断电导致没来得及还原设置）：重新打开软件再退出即可；Windows 也可以运行压缩包里的 `修复网络.bat`。

## 原理

视频号的视频没有公开的网页地址，而且文件开头经过加密。软件在本机开一个代理，**只处理视频号相关的几个域名**（`channels.weixin.qq.com`、`res.wx.qq.com`、`*.video.qq.com`），其它网站原样放行；如果你原来开着别的代理，其它流量会继续走它。

1. 在视频号页面里加一小段脚本，拿到视频地址和解密钥匙（decodeKey）
2. 下载视频，用 ISAAC64 算法生成密钥流，解密文件开头 128KB，得到正常的 MP4
3. 链接下载：通过视频号公开的预览接口拿到视频 ID，再让微信打开它，走同样的流程

为了能读取 HTTPS 页面，软件会生成一个只在你电脑上使用的根证书（私钥不会离开本机）。退出软件或关闭嗅探时，系统代理会还原；软件被强制结束时，后台守护进程也会还原。

## 开发

需要 Go 1.24+；Mac 打包需要 Xcode 命令行工具。

```bash
SAMPLE_MP4=任意大于128KB的mp4 go test ./...   # 测试
./build-mac.sh                                  # 只打包 Mac
./release.sh                                    # 打包两个平台到 dist/
./release.sh --publish "更新说明"               # 打包并发布到 GitHub Release（软件会自动更新）
```

发新版：改 `main.go` 里的 `version`，然后 `./release.sh --publish "..."`。

## 致谢

页面脚本的拦截位置和解密算法参考了 [ltaoo/wx_channels_download](https://github.com/ltaoo/wx_channels_download) 和 [Hanson/WechatSphDecrypt](https://github.com/Hanson/WechatSphDecrypt)。

## 声明

仅供个人学习研究使用。下载的视频版权归原作者所有，请勿用于商业用途或二次传播。
