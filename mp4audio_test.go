package main

import (
	"os"
	"path/filepath"
	"testing"
)

// AUDIO_DIR=一批 mp4 的文件夹 go test -run TestDecodeMP4Audio -v
func TestDecodeMP4Audio(t *testing.T) {
	dir := os.Getenv("AUDIO_DIR")
	if dir == "" {
		t.Skip("没给 AUDIO_DIR")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.mp4"))
	out := os.Getenv("WAV_OUT")
	for i, f := range files {
		pcm, rate, err := decodeMP4Audio(f)
		if err != nil {
			t.Errorf("%s：%v", filepath.Base(f), err)
			continue
		}
		t.Logf("%s：%d Hz，%.1f 秒", filepath.Base(f), rate, float64(len(pcm))/float64(rate))
		if out != "" {
			writeWav(filepath.Join(out, string(rune('a'+i))+".wav"), pcm, rate)
		}
	}
}

func TestCoreASC(t *testing.T) {
	// HE-AAC 48k 立体声（扩展层 48k，基础层 24k）：aot5 sfi=6(24k) ch2 ext=3(48k) aot2
	asc, rate, err := coreASC([]byte{0x2b, 0x11, 0x88, 0x00})
	if err != nil || rate != 24000 || len(asc) != 2 {
		t.Fatalf("HE-AAC：%v %d %x", err, rate, asc)
	}
	asc, rate, err = coreASC([]byte{0x11, 0x90}) // LC 48k 立体声
	if err != nil || rate != 48000 || asc[0] != 0x11 || asc[1] != 0x90 {
		t.Fatalf("LC：%v %d %x", err, rate, asc)
	}
}
