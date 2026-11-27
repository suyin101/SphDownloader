package main

// wxswitch.go 一键“切回网页模式”：微信 4.2 起按账号灰度用 LiteApp（自带播放器）渲染视频号，网页脚本没法注入，
// 只能靠扫内存找钥匙（慢、分不清正在看和预加载）。微信的开关在 engineConfig.json 的 featureSwitch.handleFinder，
// 改成 false 并重启微信，视频号就回到网页，原来的网页模式（秒出、整页视频带钥匙）又能用了。
// 参考 ltaoo/wx_channels_download #604/#623（Windows 4.2.0.53 多人实测有效）、foxnangang/fox-wx-channel 给的路径：
//   Windows %APPDATA%\Tencent\xwechat\radium\users\<账号>\xworker\liteapp\local\packages\<包>\engineConfig\<版本>\dist\engineConfig.json
//   Mac     ~/Library/Containers/com.tencent.xinWeChat/Data/Documents/app_data/radium/users/…（同样结构）
// 只改 "handleFinder": true → false 这一处，别的原样保留；第一次改之前备份成 .sphdl-bak。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var reHandleFinderOn = regexp.MustCompile(`("handleFinder"\s*:\s*)true\b`)

func radiumRoot() string {
	switch runtime.GOOS {
	case "windows":
		d, err := os.UserConfigDir() // %APPDATA%
		if err != nil {
			return ""
		}
		return filepath.Join(d, "Tencent", "xwechat", "radium")
	case "darwin":
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(h, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Documents", "app_data", "radium")
	}
	return ""
}

var radiumRootFn = radiumRoot

// findEngineConfigs 找所有账号、所有版本的 engineConfig.json。先按已知结构找，找不到再在 radium 下有限深度地搜。
func findEngineConfigs() ([]string, error) {
	root := radiumRootFn()
	if root == "" {
		return nil, errors.New("这个系统不支持")
	}
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, errPermission
		}
		return nil, nil // 没有这个目录：微信版本较老，没有 LiteApp
	}
	pattern := filepath.Join(root, "users", "*", "xworker", "liteapp", "local", "packages", "*", "engineConfig", "*", "dist", "engineConfig.json")
	found, _ := filepath.Glob(pattern)
	if len(found) > 0 {
		return found, nil
	}
	var out []string
	baseDepth := strings.Count(root, string(os.PathSeparator))
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) && p == root {
				return errPermission
			}
			return nil
		}
		if d.IsDir() {
			if strings.Count(p, string(os.PathSeparator))-baseDepth > 12 || d.Name() == "Cache" || d.Name() == "Code Cache" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "engineConfig.json" {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

var errPermission = errors.New("没有权限读取微信的数据")

// LiteSwitch 界面显示用的状态。
type LiteSwitch struct {
	Files  int    `json:"files"`  // 找到几个配置文件
	On     bool   `json:"on"`     // 有文件开着 LiteApp（handleFinder=true）
	Status string `json:"status"` // ""、waiting 等你退出微信、done 已切回、error
	Err    string `json:"err,omitempty"`
	Perm   bool   `json:"perm,omitempty"` // Mac 上没给“访问其他 App 数据”的权限
}

type liteSwitcher struct {
	mu      sync.Mutex
	st      LiteSwitch
	checked time.Time
	running bool
	asked   bool // Mac：读微信数据会弹系统权限框，用户点过按钮才去读
	lastSig string
}

var liteSw = &liteSwitcher{}

// State 最多 10 秒重新看一次文件（界面每次刷新都会问）。
func (l *liteSwitcher) State() LiteSwitch {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running && time.Since(l.checked) > 10*time.Second && (runtime.GOOS != "darwin" || l.asked) {
		l.checked = time.Now()
		files, err := findEngineConfigs()
		l.st.Files, l.st.On, l.st.Perm = len(files), false, errors.Is(err, errPermission)
		on := 0
		for _, f := range files {
			if b, e := os.ReadFile(f); e == nil && reHandleFinderOn.Match(b) {
				l.st.On = true
				on++
			}
		}
		if sig := fmt.Sprintf("%d/%d/%v", len(files), on, err); sig != l.lastSig { // 变了才记，排查 Windows 上找没找到配置文件
			l.lastSig = sig
			tracef("[切回网页模式] 微信配置文件 %d 个，开着自带播放器的 %d 个，err=%v，目录 %s", len(files), on, err, radiumRootFn())
		}
	}
	return l.st
}

func (l *liteSwitcher) set(f func(s *LiteSwitch)) {
	l.mu.Lock()
	f(&l.st)
	l.mu.Unlock()
}

// patchEngineConfigs 把所有开着的改成 false。返回改了几个。
func patchEngineConfigs() (int, error) {
	files, err := findEngineConfigs()
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, errors.New("没找到微信的视频号配置文件（engineConfig.json）")
	}
	n := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return n, err
		}
		if !reHandleFinderOn.Match(b) {
			continue
		}
		nb := reHandleFinderOn.ReplaceAll(b, []byte("${1}false"))
		if json.Valid(b) && !json.Valid(nb) {
			return n, fmt.Errorf("改完格式不对，没保存：%s", f)
		}
		if _, err := os.Stat(f + ".sphdl-bak"); os.IsNotExist(err) {
			if err := os.WriteFile(f+".sphdl-bak", b, 0o644); err != nil {
				return n, fmt.Errorf("备份失败：%v", err)
			}
		}
		tmp := f + ".sphdl-tmp"
		if err := os.WriteFile(tmp, nb, 0o644); err != nil {
			return n, err
		}
		if err := os.Rename(tmp, f); err != nil {
			os.Remove(tmp)
			return n, err
		}
		tracef("[切回网页模式] 已改 %s", f)
		n++
	}
	return n, nil
}

// Start 用户点了“切回网页模式”：微信开着就等用户退出（最多 5 分钟），然后改配置、重新打开微信。
func (l *liteSwitcher) Start(onDone func()) error {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return nil
	}
	l.running, l.asked = true, true
	l.st.Status, l.st.Err = "", ""
	l.mu.Unlock()
	go func() {
		defer func() {
			l.mu.Lock()
			l.running, l.checked = false, time.Time{}
			l.mu.Unlock()
		}()
		exe := wechatExePath()
		if wechatRunning() {
			l.set(func(s *LiteSwitch) { s.Status = "waiting" })
			logf("切回网页模式：请完全退出微信（托盘/菜单栏微信图标 → 退出），退出后会自动修改并重新打开微信")
			deadline := time.Now().Add(5 * time.Minute)
			for wechatRunning() {
				if time.Now().After(deadline) {
					l.set(func(s *LiteSwitch) {
						s.Status, s.Err = "error", "等了 5 分钟微信还没退出，点按钮再试一次"
					})
					return
				}
				time.Sleep(time.Second)
			}
			time.Sleep(1500 * time.Millisecond) // 等微信把文件都写完
		}
		n, err := patchEngineConfigs()
		if err != nil {
			l.set(func(s *LiteSwitch) {
				s.Status, s.Err = "error", err.Error()
				s.Perm = errors.Is(err, errPermission)
			})
			logf("切回网页模式失败：%v", err)
			return
		}
		logf("已切回网页模式（改了 %d 个配置文件，原文件备份为 .sphdl-bak），正在重新打开微信", n)
		l.set(func(s *LiteSwitch) { s.Status, s.On = "done", false })
		openWeChat(exe)
		if onDone != nil {
			onDone()
		}
	}()
	return nil
}

// watch 启动时改一次（微信没开的话），之后每 5 秒看一眼：微信从开着变成退出了，就再改一次。
// 微信开着的时候不动它的文件（它可能正在用，改了也要重启才生效）。
var (
	wechatRunningFn = wechatRunning
	watchEvery      = 5 * time.Second
	watchSettle     = 2 * time.Second
)

func (l *liteSwitcher) watch() {
	l.autoReapply()
	was := wechatRunningFn()
	for {
		time.Sleep(watchEvery)
		now := wechatRunningFn()
		if was && !now {
			l.mu.Lock()
			busy := l.running // 用户点了按钮、Start 正在处理，别抢
			l.mu.Unlock()
			if !busy {
				time.Sleep(watchSettle) // 等微信把文件写完
				l.autoReapply()
			}
		}
		was = now
	}
}

// autoReapply 用户以前切过：软件启动时如果微信没开、开关又被微信改回去了（升级后会这样），悄悄再改一次。
func (l *liteSwitcher) autoReapply() {
	if wechatRunningFn() {
		return
	}
	if n, err := patchEngineConfigs(); err == nil && n > 0 {
		logf("微信的视频号是自带播放器模式，已自动改成网页模式（%d 个文件），下次打开微信生效", n)
		l.mu.Lock()
		l.checked = time.Time{} // 界面马上重新看状态
		l.mu.Unlock()
	}
}
