package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 检查更新：读 GitHub 上最新的 Release，比版本号；有新版就下载对应平台的文件替换自己并重启。
const updateRepo = "suyin101/SphDownloader"

var updateAPI = "https://api.github.com/repos/" + updateRepo + "/releases/latest"

// 各平台在 Release 里的文件名
func updateAssetName() string {
	switch runtime.GOOS {
	case "windows":
		return "SphDownloader-windows.exe"
	case "darwin":
		return "SphDownloader-mac.zip"
	}
	return ""
}

type UpdateState struct {
	Status   string `json:"status"` // idle / checking / latest / available / downloading / restarting / error
	Current  string `json:"current"`
	Latest   string `json:"latest"`
	Notes    string `json:"notes"`
	Page     string `json:"page"`
	Progress int64  `json:"progress"`
	Total    int64  `json:"total"`
	Err      string `json:"err"`
	Checked  int64  `json:"checked"`
	assetURL string
}

type Updater struct {
	mu sync.Mutex
	st UpdateState
}

func NewUpdater() *Updater { return &Updater{st: UpdateState{Status: "idle", Current: version}} }

func (u *Updater) State() UpdateState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *Updater) set(f func(s *UpdateState)) {
	u.mu.Lock()
	f(&u.st)
	u.mu.Unlock()
}

// newerVersion a 是否比 b 新（1.10.0 > 1.9.3）。
func newerVersion(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// githubClient 访问 GitHub 时走用户自己原来的代理（梯子），没有就直连。
func githubClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
			if up := currentUpstream(); up != "" {
				return url.Parse(up)
			}
			return nil, nil
		}},
	}
}

func currentUpstream() string {
	if sniffer != nil && sniffer.On() {
		return sniffer.Upstream()
	}
	return upstreamFromState(readProxy())
}

func (u *Updater) Check() error {
	u.set(func(s *UpdateState) { s.Status, s.Err = "checking", "" })
	err := u.check()
	u.set(func(s *UpdateState) {
		s.Checked = time.Now().Unix()
		if err != nil {
			s.Status, s.Err = "error", err.Error()
		}
	})
	return err
}

func (u *Updater) check() error {
	req, _ := http.NewRequest("GET", updateAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "SphDownloader/"+version)
	resp, err := githubClient(20 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("连不上 GitHub：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return fmt.Errorf("还没有发布过版本")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return fmt.Errorf("版本信息格式不对：%v", err)
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	asset, size := "", int64(0)
	for _, a := range rel.Assets {
		if a.Name == updateAssetName() {
			asset, size = a.URL, a.Size
		}
	}
	u.set(func(s *UpdateState) {
		s.Latest, s.Notes, s.Page, s.assetURL, s.Total = latest, rel.Body, rel.HTMLURL, asset, size
		if newerVersion(latest, version) && asset != "" {
			s.Status = "available"
		} else {
			s.Status = "latest"
		}
	})
	if newerVersion(latest, version) {
		logf("发现新版本 v%s", latest)
	}
	return nil
}

// Apply 下载新版本、替换自己、重启。
func (u *Updater) Apply() error {
	st := u.State()
	if st.assetURL == "" {
		return fmt.Errorf("没有可用的更新")
	}
	if st.Status == "downloading" || st.Status == "restarting" {
		return nil
	}
	target, err := updateDownloadPath()
	if err != nil {
		u.set(func(s *UpdateState) { s.Status, s.Err = "error", err.Error() })
		return err
	}
	u.set(func(s *UpdateState) { s.Status, s.Progress, s.Err = "downloading", 0, "" })
	logf("正在下载新版本 v%s…", st.Latest)
	if err := u.download(st.assetURL, target); err != nil {
		os.Remove(target)
		u.set(func(s *UpdateState) { s.Status, s.Err = "error", "下载新版本失败："+err.Error() })
		return err
	}
	u.set(func(s *UpdateState) { s.Status = "restarting" })
	logf("新版本已下载，正在重启…")
	if err := installUpdate(target); err != nil {
		os.Remove(target)
		u.set(func(s *UpdateState) { s.Status, s.Err = "error", "安装新版本失败："+err.Error() })
		return err
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		if quitHook != nil {
			quitHook()
			return
		}
		quit()
	}()
	return nil
}

var quitHook func() // 测试用

func (u *Updater) download(src, dst string) error {
	req, _ := http.NewRequest("GET", src, nil)
	req.Header.Set("User-Agent", "SphDownloader/"+version)
	resp, err := githubClient(30 * time.Minute).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 0 {
		u.set(func(s *UpdateState) { s.Total = resp.ContentLength })
	}
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("没有写入权限（%v）。请把软件放到“下载”或桌面等自己的文件夹里", err)
	}
	_, err = io.Copy(f, &progressReader{r: resp.Body, onRead: func(n int64) {
		u.set(func(s *UpdateState) { s.Progress += n })
	}})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
