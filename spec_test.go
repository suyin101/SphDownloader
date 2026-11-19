package main

import (
	"strings"
	"testing"
)

func TestBestSpec(t *testing.T) {
	specs := []Spec{
		{FileFormat: "xWT113", Width: 540, Height: 960, LevelOrder: 3, Bitrate: 500},
		{FileFormat: "xWT111", Width: 1080, Height: 1920, LevelOrder: 1, Bitrate: 2000},
		{FileFormat: "xWT112", Width: 720, Height: 1280, LevelOrder: 2, Bitrate: 1000},
		{FileFormat: "xWT999", Width: 2160, Height: 3840, LevelOrder: 0},
	}
	if b := bestSpec(specs); b.FileFormat != "xWT111" {
		t.Fatalf("got %s", b.FileFormat)
	}
	// 都没有 levelOrder 时按分辨率
	noLevel := []Spec{{FileFormat: "a", Width: 720, Height: 1280}, {FileFormat: "b", Width: 1080, Height: 1920}}
	if b := bestSpec(noLevel); b.FileFormat != "b" {
		t.Fatalf("got %s", b.FileFormat)
	}
	if bestSpec(nil) != nil {
		t.Fatal("nil specs")
	}
}

func TestCandidateURLs(t *testing.T) {
	f := FeedInfo{URL: "https://finder.video.qq.com/251/20302/stodownload?encfilekey=K&token=T&idx=1&X-snsvideoflag=xWT113",
		Specs: []Spec{{FileFormat: "xWT111", LevelOrder: 1}, {FileFormat: "xWT113", LevelOrder: 3}}}
	u := candidateURLs(f)
	if len(u) != 3 || !strings.Contains(u[0], "X-snsvideoflag=xWT111") || strings.Contains(u[0], "xWT113") || !strings.Contains(u[0], "idx=1") {
		t.Fatalf("%v", u)
	}
	if u[1] != "https://finder.video.qq.com/251/20302/stodownload?encfilekey=K&token=T" {
		t.Fatalf("%v", u[1])
	}
	if got := candidateURLs(FeedInfo{URL: "https://x/a?encfilekey=K&token=T"}); len(got) != 2 {
		t.Fatalf("%v", got)
	}
}
