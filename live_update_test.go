package main

import (
	"os"
	"testing"
)

// LIVE=1：对真实的 GitHub Release 检查更新
func TestUpdateCheckLive(t *testing.T) {
	if os.Getenv("LIVE") != "1" {
		t.Skip()
	}
	u := NewUpdater()
	if err := u.Check(); err != nil {
		t.Fatal(err)
	}
	st := u.State()
	t.Logf("latest=%s status=%s asset=%v", st.Latest, st.Status, st.assetURL != "")
	if st.Latest == "" || st.assetURL == "" {
		t.Fatal("no release/asset found")
	}
}
