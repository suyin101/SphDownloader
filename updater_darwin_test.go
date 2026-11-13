package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// 模拟 GitHub：有新版本 → 下载 → (Mac) 替换 .app → 重新打开
func TestUpdateFlow(t *testing.T) {
	dir := t.TempDir()
	// 做一个“新版本”的 .app 打成 zip
	newApp := filepath.Join(dir, "src", "视频号下载器.app", "Contents", "MacOS")
	os.MkdirAll(newApp, 0o755)
	os.WriteFile(filepath.Join(newApp, "SphDownloader"), []byte("NEW"), 0o755)
	zip := filepath.Join(dir, "SphDownloader-mac.zip")
	if out, err := exec.Command("ditto", "-c", "-k", "--keepParent", filepath.Join(dir, "src", "视频号下载器.app"), zip).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	zipData, _ := os.ReadFile(zip)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","body":"修复了一些问题\n第二行","html_url":"x","assets":[{"name":"SphDownloader-mac.zip","browser_download_url":"%s/dl","size":%d},{"name":"SphDownloader-windows.exe","browser_download_url":"%s/exe","size":3}]}`, srv.URL, len(zipData), srv.URL)
			return
		}
		w.Write(zipData)
	}))
	defer srv.Close()
	oldAPI := updateAPI
	updateAPI = srv.URL + "/latest"
	defer func() { updateAPI = oldAPI }()

	// “正在运行”的旧版本 .app
	installed := filepath.Join(dir, "Applications", "视频号下载器.app")
	os.MkdirAll(filepath.Join(installed, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(installed, "Contents", "MacOS", "SphDownloader"), []byte("OLD"), 0o755)
	appBundle = func() string { return installed }
	relaunched := ""
	relaunch = func(app string) error { relaunched = app; return nil }
	defer func() { appBundle = runningAppBundle }()

	u := NewUpdater()
	if err := u.Check(); err != nil {
		t.Fatal(err)
	}
	if st := u.State(); st.Status != "available" || st.Latest != "9.9.9" {
		t.Fatalf("%+v", st)
	}
	quitCalled := make(chan bool, 1)
	quitHook = func() { quitCalled <- true }
	defer func() { quitHook = nil }()
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(installed, "Contents", "MacOS", "SphDownloader"))
	if string(b) != "NEW" {
		t.Fatalf("app not replaced: %q", b)
	}
	if relaunched != installed {
		t.Fatal("should relaunch new app")
	}
	<-quitCalled
	olds, _ := filepath.Glob(filepath.Join(dir, "Applications", ".SphDownloader-old-*.app"))
	if len(olds) != 1 {
		t.Fatal("old app should be kept until next start")
	}
	cleanupOldUpdate()
	olds, _ = filepath.Glob(filepath.Join(dir, "Applications", ".SphDownloader-*"))
	if len(olds) != 0 {
		t.Fatalf("leftovers: %v", olds)
	}
}
