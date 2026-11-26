package main

// mp4audio.go 从 mp4 里取出 AAC 声音并解码成单声道 PCM（纯 Go，不用 ffmpeg），给转文字用。
// HE-AAC / HE-AACv2 只解基础层（采样率减半、单声道），对识别人声没影响。

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/skrashevich/go-aac/pkg/decoder"
)

type mp4Box struct {
	typ        string
	start, end int64 // 内容的起止位置（不含头）
}

type audioTrack struct {
	asc     []byte
	sizes   []uint32
	offsets []int64 // 每个 chunk 的位置
	stsc    [][3]uint32
	tsRate  uint32
}

func readBoxes(f io.ReaderAt, start, end int64) ([]mp4Box, error) {
	var out []mp4Box
	hdr := make([]byte, 16)
	for pos := start; pos+8 <= end; {
		if _, err := f.ReadAt(hdr[:8], pos); err != nil {
			return out, err
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		head := int64(8)
		if size == 1 {
			if _, err := f.ReadAt(hdr[8:16], pos+8); err != nil {
				return out, err
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:16]))
			head = 16
		} else if size == 0 {
			size = end - pos
		}
		if size < head || pos+size > end {
			break
		}
		out = append(out, mp4Box{typ, pos + head, pos + size})
		pos += size
	}
	return out, nil
}

func findBox(f io.ReaderAt, bs []mp4Box, path ...string) (mp4Box, bool) {
	for _, b := range bs {
		if b.typ != path[0] {
			continue
		}
		if len(path) == 1 {
			return b, true
		}
		kids, _ := readBoxes(f, b.start, b.end)
		if r, ok := findBox(f, kids, path[1:]...); ok {
			return r, true
		}
	}
	return mp4Box{}, false
}

func boxBytes(f io.ReaderAt, b mp4Box, max int64) ([]byte, error) {
	n := b.end - b.start
	if n > max {
		return nil, fmt.Errorf("%s 太大", b.typ)
	}
	buf := make([]byte, n)
	_, err := f.ReadAt(buf, b.start)
	return buf, err
}

// findAudioTrack 找到声音轨（hdlr=soun 且是 mp4a）。
func findAudioTrack(f io.ReaderAt, size int64) (*audioTrack, error) {
	top, _ := readBoxes(f, 0, size)
	moov, ok := findBox(f, top, "moov")
	if !ok {
		return nil, errors.New("视频文件里没有 moov（文件不完整？）")
	}
	traks, _ := readBoxes(f, moov.start, moov.end)
	for _, tk := range traks {
		if tk.typ != "trak" {
			continue
		}
		kids, _ := readBoxes(f, tk.start, tk.end)
		hdlr, ok := findBox(f, kids, "mdia", "hdlr")
		if !ok {
			continue
		}
		hb, err := boxBytes(f, hdlr, 1<<16)
		if err != nil || len(hb) < 12 || string(hb[8:12]) != "soun" {
			continue
		}
		stbl, ok := findBox(f, kids, "mdia", "minf", "stbl")
		if !ok {
			continue
		}
		return parseStbl(f, stbl, kids)
	}
	return nil, errNoAudio
}

var errNoAudio = errors.New("这个视频没有声音")

func parseStbl(f io.ReaderAt, stbl mp4Box, trak []mp4Box) (*audioTrack, error) {
	t := &audioTrack{}
	if mdhd, ok := findBox(f, trak, "mdia", "mdhd"); ok {
		if b, err := boxBytes(f, mdhd, 1<<10); err == nil && len(b) >= 24 {
			if b[0] == 1 && len(b) >= 24 {
				t.tsRate = binary.BigEndian.Uint32(b[20:24])
			} else {
				t.tsRate = binary.BigEndian.Uint32(b[12:16])
			}
		}
	}
	kids, _ := readBoxes(f, stbl.start, stbl.end)
	for _, k := range kids {
		switch k.typ {
		case "stsd":
			b, err := boxBytes(f, k, 1<<20)
			if err != nil {
				return nil, err
			}
			asc, err := ascFromStsd(b)
			if err != nil {
				return nil, err
			}
			t.asc = asc
		case "stsz":
			b, err := boxBytes(f, k, 1<<28)
			if err != nil || len(b) < 12 {
				return nil, errors.New("stsz 读不出来")
			}
			fixed, n := binary.BigEndian.Uint32(b[4:8]), int(binary.BigEndian.Uint32(b[8:12]))
			if fixed == 0 && len(b) < 12+4*n {
				return nil, errors.New("stsz 不完整")
			}
			t.sizes = make([]uint32, n)
			for i := range t.sizes {
				if fixed != 0 {
					t.sizes[i] = fixed
				} else {
					t.sizes[i] = binary.BigEndian.Uint32(b[12+4*i:])
				}
			}
		case "stco", "co64":
			b, err := boxBytes(f, k, 1<<28)
			if err != nil || len(b) < 8 {
				return nil, errors.New("stco 读不出来")
			}
			n := int(binary.BigEndian.Uint32(b[4:8]))
			w := 4
			if k.typ == "co64" {
				w = 8
			}
			if len(b) < 8+w*n {
				return nil, errors.New("stco 不完整")
			}
			t.offsets = make([]int64, n)
			for i := range t.offsets {
				if w == 8 {
					t.offsets[i] = int64(binary.BigEndian.Uint64(b[8+8*i:]))
				} else {
					t.offsets[i] = int64(binary.BigEndian.Uint32(b[8+4*i:]))
				}
			}
		case "stsc":
			b, err := boxBytes(f, k, 1<<26)
			if err != nil || len(b) < 8 {
				return nil, errors.New("stsc 读不出来")
			}
			n := int(binary.BigEndian.Uint32(b[4:8]))
			if len(b) < 8+12*n {
				return nil, errors.New("stsc 不完整")
			}
			for i := 0; i < n; i++ {
				p := b[8+12*i:]
				t.stsc = append(t.stsc, [3]uint32{binary.BigEndian.Uint32(p), binary.BigEndian.Uint32(p[4:]), binary.BigEndian.Uint32(p[8:])})
			}
		}
	}
	if t.asc == nil {
		return nil, errors.New("声音不是 AAC 格式，暂不支持")
	}
	if len(t.sizes) == 0 || len(t.offsets) == 0 || len(t.stsc) == 0 {
		return nil, errNoAudio
	}
	return t, nil
}

// ascFromStsd 从 stsd → mp4a → esds 里取 AudioSpecificConfig。
func ascFromStsd(b []byte) ([]byte, error) {
	// stsd: 版本4 + 数量4，然后是样本描述 box
	if len(b) < 16 || string(b[12:16]) != "mp4a" {
		return nil, errors.New("声音不是 AAC 格式，暂不支持")
	}
	entry := b[8:]
	esz := int(binary.BigEndian.Uint32(entry))
	if esz > len(entry) || esz < 36 {
		return nil, errors.New("mp4a 不完整")
	}
	entry = entry[:esz]
	// mp4a 头：8(box) + 6 保留 + 2 索引 + 8 版本等 + 2 声道 + 2 位深 + 4 + 4 采样率 = 36；v1/v2 更长
	ver := binary.BigEndian.Uint16(entry[16:18])
	p := 36
	if ver == 1 {
		p += 16
	} else if ver == 2 {
		p += 36
	}
	for p+8 <= len(entry) {
		sz := int(binary.BigEndian.Uint32(entry[p:]))
		if sz < 8 || p+sz > len(entry) {
			break
		}
		if string(entry[p+4:p+8]) == "esds" {
			return ascFromEsds(entry[p+12 : p+sz]) // 跳过版本4
		}
		p += sz
	}
	return nil, errors.New("找不到 AAC 配置（esds）")
}

func ascFromEsds(d []byte) ([]byte, error) {
	readDesc := func(p int) (tag byte, start, end int, ok bool) {
		if p >= len(d) {
			return
		}
		tag = d[p]
		p++
		n := 0
		for i := 0; i < 4 && p < len(d); i++ {
			c := d[p]
			p++
			n = n<<7 | int(c&0x7f)
			if c&0x80 == 0 {
				break
			}
		}
		if p+n > len(d) {
			n = len(d) - p
		}
		return tag, p, p + n, true
	}
	tag, s, e, ok := readDesc(0)
	if !ok || tag != 3 { // ES_Descriptor
		return nil, errors.New("esds 格式不对")
	}
	if e-s < 3 {
		return nil, errors.New("esds 格式不对")
	}
	flags := d[s+2]
	p := s + 3
	if flags&0x80 != 0 {
		p += 2
	}
	if flags&0x40 != 0 && p < len(d) {
		p += 1 + int(d[p])
	}
	if flags&0x20 != 0 {
		p += 2
	}
	tag, s, e, ok = readDesc(p)
	if !ok || tag != 4 { // DecoderConfigDescriptor
		return nil, errors.New("esds 里没有解码配置")
	}
	tag, s, e, ok = readDesc(s + 13)
	if !ok || tag != 5 || e <= s {
		return nil, errors.New("esds 里没有 AAC 配置")
	}
	return d[s:e], nil
}

// coreASC 把 HE-AAC/HE-AACv2 的配置改成只解基础层（AAC-LC），返回 2 字节配置和基础层采样率。
func coreASC(asc []byte) ([]byte, int, error) {
	br := &bitReader{b: asc}
	aot := br.read(5)
	if aot == 31 {
		aot = 32 + br.read(6)
	}
	sfi := br.read(4)
	if sfi == 15 {
		return nil, 0, errors.New("不支持的采样率")
	}
	ch := br.read(4)
	if aot == 5 || aot == 29 { // SBR / PS：后面是扩展采样率和真正的基础层类型
		br.read(4)
		aot = br.read(5)
		if aot == 29 || ch == 0 {
			ch = 1
		}
	}
	if aot != 2 && aot != 1 && aot != 4 {
		return nil, 0, fmt.Errorf("不支持的 AAC 类型 %d", aot)
	}
	if ch == 0 || ch > 2 {
		ch = 2
	}
	rates := []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
	if int(sfi) >= len(rates) {
		return nil, 0, errors.New("不支持的采样率")
	}
	v := uint16(2)<<11 | uint16(sfi)<<7 | uint16(ch)<<3
	return []byte{byte(v >> 8), byte(v)}, rates[sfi], nil
}

type bitReader struct {
	b   []byte
	pos int
}

func (r *bitReader) read(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		v <<= 1
		if r.pos/8 < len(r.b) && r.b[r.pos/8]&(0x80>>(r.pos%8)) != 0 {
			v |= 1
		}
		r.pos++
	}
	return v
}

// decodeMP4Audio 解出整段声音（单声道 int16）和采样率。
func decodeMP4Audio(path string) ([]int16, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	tr, err := findAudioTrack(f, st.Size())
	if err != nil {
		return nil, 0, err
	}
	asc, rate, err := coreASC(tr.asc)
	if err != nil {
		return nil, 0, err
	}
	dec := decoder.New()
	if err := dec.SetASC(asc); err != nil {
		return nil, 0, fmt.Errorf("声音解码器初始化失败：%v", err)
	}
	pcm := make([]int16, 0, len(tr.sizes)*1024)
	frame := make([]byte, 0, 4096)
	silence := make([]int16, 1024)
	bad := 0
	sample := 0
	for ci, off := range tr.offsets {
		chunk := uint32(ci + 1)
		per := uint32(0)
		for _, e := range tr.stsc {
			if e[0] <= chunk {
				per = e[1]
			} else {
				break
			}
		}
		pos := off
		for j := uint32(0); j < per && sample < len(tr.sizes); j++ {
			sz := int(tr.sizes[sample])
			sample++
			if sz <= 0 || sz > 1<<16 {
				pos += int64(sz)
				continue
			}
			frame = frame[:sz]
			if _, err := f.ReadAt(frame, pos); err != nil {
				return nil, 0, fmt.Errorf("读取声音出错：%v", err)
			}
			pos += int64(sz)
			out, err := safeDecode(dec, frame)
			if err != nil || len(out) == 0 {
				bad++
				pcm = append(pcm, silence...)
				continue
			}
			pcm = appendMono(pcm, out, dec.Config.ChanConfig)
		}
	}
	if sample > 0 && bad*2 > sample {
		return nil, 0, fmt.Errorf("声音解码失败（%d/%d 帧出错）", bad, sample)
	}
	return pcm, rate, nil
}

func safeDecode(dec *decoder.Decoder, frame []byte) (out []float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("解码出错：%v", r)
		}
	}()
	return dec.DecodeFrame(frame)
}

func appendMono(dst []int16, in []float32, ch int) []int16 {
	if ch < 1 {
		ch = 1
	}
	for i := 0; i+ch <= len(in); i += ch {
		var s float32
		for c := 0; c < ch; c++ {
			s += in[i+c]
		}
		s /= float32(ch)
		// 解码器输出可能是 ±1 也可能是 ±32768，统一按 int16 范围处理
		if s > -1.5 && s < 1.5 {
			s *= 32767
		}
		dst = append(dst, int16(math.Max(-32768, math.Min(32767, float64(s)))))
	}
	return dst
}

// writeWav 写 16 位单声道 wav。
func writeWav(path string, pcm []int16, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	n := len(pcm) * 2
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+n))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(n))
	if _, err := f.Write(h); err != nil {
		f.Close()
		return err
	}
	buf := make([]byte, n)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(s))
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
