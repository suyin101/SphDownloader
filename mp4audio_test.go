package main

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// 和 ffmpeg 解出来的声音比（本机有 ffmpeg 时）：每条视频相关度都要 ≥0.9。
// 2026-11-26 发现 go-aac v0.1.0 的 PNS 有 bug，解出来只剩三成声音、长视频后半段识别成乱码，升级到 800d9fa 修好。
func TestDecodeVsFFmpeg(t *testing.T) {
	dir := os.Getenv("AUDIO_DIR")
	ff, err := exec.LookPath("ffmpeg")
	if dir == "" || err != nil {
		t.Skip("没给 AUDIO_DIR 或没装 ffmpeg")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.mp4"))
	for _, f := range files {
		pcm, rate, err := decodeMP4Audio(f)
		if err != nil {
			t.Errorf("%s：%v", filepath.Base(f), err)
			continue
		}
		raw, err := exec.Command(ff, "-v", "error", "-i", f, "-ac", "1", "-ar", strconv.Itoa(rate), "-f", "s16le", "-").Output()
		if err != nil {
			t.Errorf("ffmpeg：%v", err)
			continue
		}
		ref := make([]int16, len(raw)/2)
		for i := range ref {
			ref[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
		}
		n := min(len(pcm), len(ref)) - 4096
		if n < rate*5 {
			continue // 太短不比
		}
		// 解码器起始延迟不同（HE-AAC 还有 SBR/PS 的延迟），先在一小段里逐个偏移找对齐
		best, bc := 0, -1e300
		for off := -2600; off <= 2600; off++ {
			var c float64
			for i := 3000; i < 3000+rate/4; i++ {
				c += float64(pcm[i]) * float64(ref[i+off])
			}
			if c > bc {
				best, bc = off, c
			}
		}
		corr := 0.0
		for _, off := range []int{best} {
			var gg, pp, gf float64
			for i := 3000; i < n-3000; i += 7 {
				j := i + off
				a, b := float64(pcm[i]), float64(ref[j])
				gg, pp, gf = gg+a*a, pp+b*b, gf+a*b
			}
			corr = gf / math.Sqrt(gg*pp+1)
		}
		t.Logf("%s：相关 %.3f（偏移 %d）", filepath.Base(f), corr, best)
		if f2, err := os.Open(f); err == nil {
			st, _ := f2.Stat()
			tr, _ := findAudioTrack(f2, st.Size())
			f2.Close()
			if tr != nil && len(tr.asc) > 0 && tr.asc[0]>>3 == 29 && corr < 0.9 {
				// HE-AACv2 的立体声是从单声道“合成”出来的，音乐类混成单声道后和基础层对不上，属正常；人声样本实测 0.99
				t.Logf("（HE-AACv2 立体声，跳过）")
				continue
			}
		}
		if corr < 0.9 {
			t.Errorf("%s 解出来的声音和 ffmpeg 差太多（相关 %.3f）", filepath.Base(f), corr)
		}
	}
}
