package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	version = "1.4.2"
	uiPort  = 18889
)

var proxyPort = 18888

//go:embed ui.html
var uiHTML []byte

var (
	dataDir  string
	logMu    sync.Mutex
	logLines []string
)

func logf(format string, a ...any) {
	line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, a...)
	fmt.Println(line)
	tracef("[日志] %s", fmt.Sprintf(format, a...))
	logMu.Lock()
	logLines = append(logLines, line)
	if len(logLines) > 300 {
		logLines = logLines[len(logLines)-300:]
	}
	logMu.Unlock()
}

type config struct {
	Dir           string `json:"dir"`
	AutoDownload  *bool  `json:"autoDownload"`
	DownloadCover bool   `json:"downloadCover"`
	Sniff         *bool  `json:"sniff"`
	AutoUpdate    *bool  `json:"autoUpdate"`
}

func loadConfig() config {
	var c config
	if b, err := os.ReadFile(filepath.Join(dataDir, "config.json")); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Dir == "" {
		home, _ := os.UserHomeDir()
		c.Dir = filepath.Join(home, "Downloads", "视频号下载")
	}
	return c
}

func saveConfig(c config) {
	b, _ := json.MarshalIndent(c, "", "  ")
	os.WriteFile(filepath.Join(dataDir, "config.json"), b, 0o644)
}

// 系统代理的备份：万一程序被强制结束，守护进程或下次启动能还原成原来的设置。
// 同时记下是哪个进程设的代理，守护进程只还原自己主程序设的（升级时新旧版本交接不会互相干扰）。
func saveProxyBackup(s proxyState) {
	b, _ := json.Marshal(s)
	os.WriteFile(filepath.Join(dataDir, "proxy-backup.json"), b, 0o644)
	os.WriteFile(filepath.Join(dataDir, "proxy-owner"), []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func proxyOwner() int {
	b, _ := os.ReadFile(filepath.Join(dataDir, "proxy-owner"))
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func loadProxyBackup() (proxyState, bool) {
	var s proxyState
	b, err := os.ReadFile(filepath.Join(dataDir, "proxy-backup.json"))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return s, false
	}
	return s, true
}

func removeProxyBackup() {
	os.Remove(filepath.Join(dataDir, "proxy-backup.json"))
	os.Remove(filepath.Join(dataDir, "proxy-owner"))
}

func fatal(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fmt.Printf("\n出错了：%s\n", msg)
	restoreSystemProxy()
	alert("视频号下载器出错了：\n" + msg)
	os.Exit(1)
}

func main() {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		cfgDir = "."
	}
	dataDir = filepath.Join(cfgDir, "SphDownloader")
	os.MkdirAll(dataDir, 0o755)

	if len(os.Args) == 3 && os.Args[1] == "--guard" {
		runGuard(os.Args[2])
		return
	}
	setupConsole()
	fmt.Printf("视频号下载器 v%s\n\n", version)

	uiURL := fmt.Sprintf("http://127.0.0.1:%d/", uiPort)
	uiLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	if err != nil && takeOverOldVersion() {
		uiLn, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	}
	if err != nil {
		// 已经开着同一版本：只再开一个窗口，不动网络设置
		runWindow(uiURL)
		return
	}

	ca, caPath, err := loadOrCreateCA(dataDir)
	if err != nil {
		fatal("生成证书失败：%v", err)
	}
	if !certInstalled() {
		if certHint != "" {
			alert(certHint)
		}
		if err := installCert(caPath); err != nil || !certInstalled() {
			fatal("证书没装上（是不是点了取消？）重新打开软件再试一次。%v", err)
		}
		logf("证书已安装")
	}

	cfg := loadConfig()
	store := NewStore(cfg.Dir)
	if cfg.AutoDownload != nil {
		store.AutoDownload = *cfg.AutoDownload
	}
	store.DownloadCover = cfg.DownloadCover
	store.loadHistory(filepath.Join(dataDir, "history.json"))

	sniffer = &Sniffer{ca: ca, store: store}
	startGuard()
	if cfg.Sniff == nil || *cfg.Sniff {
		if err := sniffer.Start(); err != nil {
			logf("嗅探没开起来：%v", err)
		}
	} else {
		cleanupStaleProxy() // 上次异常退出留下的代理也一并清掉
		logf("嗅探是关闭状态，需要时点右上角开关打开")
	}
	cleanupOldUpdate()
	updater = NewUpdater()
	go http.Serve(uiLn, uiHandler(store, &cfg))
	go autoUpdateLoop(store, &cfg)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		quit()
	}()

	logf("已启动 v%s。保存位置：%s", version, cfg.Dir)
	runWindow(uiURL) // 关掉窗口就退出
	quit()
}

// takeOverOldVersion 后台还开着旧版本时，让它退出（它会先还原网络设置），换新版本接手。
func takeOverOldVersion() bool {
	c := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/state", uiPort))
	if err != nil {
		return false
	}
	var st struct {
		Version string `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st.Version == "" || st.Version == version {
		return false
	}
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/quit", uiPort), strings.NewReader("{}"))
	req.Header.Set("X-Sphdl", "1")
	if r, err := c.Do(req); err == nil {
		r.Body.Close()
	}
	for i := 0; i < 50; i++ { // 最多等 5 秒
		time.Sleep(100 * time.Millisecond)
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort)); err == nil {
			l.Close()
			logf("已关闭旧版本 v%s", st.Version)
			return true
		}
	}
	return false
}

var (
	quitOnce sync.Once
	sniffer  *Sniffer
	updater  *Updater
)

// autoUpdateLoop 启动后检查一次更新，之后每 6 小时检查一次。
// 开着“自动更新”时，只在启动时、且没有正在下载的视频时自动安装，免得打断你。
func autoUpdateLoop(store *Store, cfg *config) {
	time.Sleep(3 * time.Second)
	first := true
	for {
		if updater.Check() == nil && updater.State().Status == "available" {
			auto := cfg.AutoUpdate == nil || *cfg.AutoUpdate
			if first && auto && store.Busy() == 0 {
				logf("开着自动更新，开始安装新版本")
				updater.Apply()
			}
		}
		first = false
		time.Sleep(6 * time.Hour)
	}
}

func quit() {
	quitOnce.Do(func() {
		if sniffer != nil {
			sniffer.Stop()
		}
		restoreSystemProxy()
		fmt.Println("已恢复网络设置，再见。")
		os.Exit(0)
	})
}

type apiBody struct {
	Keys       []string `json:"keys"`
	Text       string   `json:"text"`
	File       string   `json:"file"`
	Auto       *bool    `json:"auto"`
	Cover      *bool    `json:"cover"`
	On         *bool    `json:"on"`
	AutoUpdate *bool    `json:"autoUpdate"`
}

func uiHandler(store *Store, cfg *config) http.Handler {
	mux := http.NewServeMux()
	var cfgMu sync.Mutex
	save := func(f func(c *config)) {
		cfgMu.Lock()
		f(cfg)
		saveConfig(*cfg)
		cfgMu.Unlock()
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(uiHTML)
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		items, gotCurrent := store.Snapshot()
		logMu.Lock()
		logs := append([]string(nil), logLines...)
		logMu.Unlock()
		store.mu.Lock()
		dir, auto, cover := store.Dir, store.AutoDownload, store.DownloadCover
		store.mu.Unlock()
		cfgMu.Lock()
		autoUpd := cfg.AutoUpdate == nil || *cfg.AutoUpdate
		cfgMu.Unlock()
		var upd any
		if updater != nil {
			upd = updater.State()
		}
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]any{
			"version": version, "items": items, "logs": logs, "dir": dir,
			"auto": auto, "cover": cover, "sniff": sniffer.On(), "sniffErr": sniffer.LastErr(), "online": store.PageState() == "online", "page": store.PageState(), "currentKey": store.CurrentKey(), "update": upd, "autoUpdate": autoUpd, "gotCurrent": gotCurrent,
		})
	})
	mux.HandleFunc("/api/preview", func(w http.ResponseWriter, r *http.Request) {
		store.ServePreview(w, r, r.URL.Query().Get("key"))
	})
	// 改动类操作只接受本软件页面发起的 POST（带自定义头，别的网页跨域发不了）
	post := func(f func(b apiBody) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.Header.Get("X-Sphdl") != "1" {
				http.Error(w, "forbidden", 403)
				return
			}
			var b apiBody
			json.NewDecoder(r.Body).Decode(&b)
			res, err := f(b)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if res == nil {
				res = map[string]bool{"ok": true}
			}
			json.NewEncoder(w).Encode(res)
		}
	}
	mux.HandleFunc("/api/download", post(func(b apiBody) (any, error) {
		return map[string]int{"n": store.Enqueue(b.Keys)}, nil
	}))
	mux.HandleFunc("/api/cover", post(func(b apiBody) (any, error) {
		if len(b.Keys) == 1 {
			p, err := store.SaveCover(b.Keys[0])
			return map[string]string{"file": p}, err
		}
		go func() {
			for _, k := range b.Keys {
				if _, err := store.SaveCover(k); err != nil {
					logf("封面保存失败：%v", err)
				}
			}
		}()
		return nil, nil
	}))
	mux.HandleFunc("/api/clear", post(func(b apiBody) (any, error) {
		return map[string]int{"n": store.Clear()}, nil
	}))
	mux.HandleFunc("/api/sniff", post(func(b apiBody) (any, error) {
		on := b.On != nil && *b.On
		if on {
			if err := sniffer.Start(); err != nil {
				return nil, err
			}
		} else {
			sniffer.Stop()
		}
		save(func(c *config) { c.Sniff = &on })
		return nil, nil
	}))
	mux.HandleFunc("/api/remove", post(func(b apiBody) (any, error) {
		store.Remove(b.Keys)
		return nil, nil
	}))
	mux.HandleFunc("/api/links", post(func(b apiBody) (any, error) {
		n := store.AddLinks(b.Text)
		if n == 0 {
			return nil, fmt.Errorf("没找到新的视频号链接。支持 https://weixin.qq.com/sph/xxxx 这种格式")
		}
		logf("添加了 %d 个链接", n)
		return map[string]int{"n": n}, nil
	}))
	mux.HandleFunc("/api/settings", post(func(b apiBody) (any, error) {
		store.mu.Lock()
		if b.Auto != nil {
			store.AutoDownload = *b.Auto
		}
		if b.Cover != nil {
			store.DownloadCover = *b.Cover
		}
		auto, cover := store.AutoDownload, store.DownloadCover
		store.mu.Unlock()
		save(func(c *config) {
			c.AutoDownload = &auto
			c.DownloadCover = cover
			if b.AutoUpdate != nil {
				c.AutoUpdate = b.AutoUpdate
			}
		})
		return nil, nil
	}))
	mux.HandleFunc("/api/choosedir", post(func(b apiBody) (any, error) {
		store.mu.Lock()
		cur := store.Dir
		store.mu.Unlock()
		d := chooseFolder(cur)
		if d == "" {
			return map[string]string{"dir": cur}, nil
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("用不了这个文件夹：%v", err)
		}
		store.mu.Lock()
		store.Dir = d
		store.mu.Unlock()
		save(func(c *config) { c.Dir = d })
		logf("保存位置改为：%s", d)
		return map[string]string{"dir": d}, nil
	}))
	mux.HandleFunc("/api/open", post(func(b apiBody) (any, error) {
		if f := strings.TrimSpace(b.File); f != "" {
			if _, err := os.Stat(f); err != nil {
				return nil, fmt.Errorf("文件不见了，可能被移走或删除了")
			}
			revealFile(f)
			return nil, nil
		}
		store.mu.Lock()
		d := store.Dir
		store.mu.Unlock()
		os.MkdirAll(d, 0o755)
		openFolder(d)
		return nil, nil
	}))
	mux.HandleFunc("/api/clipboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Sphdl") != "1" {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(readClipboard()))
	})
	mux.HandleFunc("/api/checkupdate", post(func(b apiBody) (any, error) {
		if err := updater.Check(); err != nil {
			return nil, err
		}
		return updater.State(), nil
	}))
	mux.HandleFunc("/api/applyupdate", post(func(b apiBody) (any, error) {
		go updater.Apply()
		return nil, nil
	}))
	mux.HandleFunc("/api/logs", post(func(b apiBody) (any, error) {
		d := filepath.Join(dataDir, "debug")
		os.MkdirAll(d, 0o755)
		openFolder(d)
		return nil, nil
	}))
	mux.HandleFunc("/api/quit", post(func(b apiBody) (any, error) {
		go func() { time.Sleep(300 * time.Millisecond); quit() }()
		return nil, nil
	}))
	return mux
}
