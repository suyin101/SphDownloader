package main

// 视频号视频的加密方式：文件开头 131072 字节和 ISAAC64 随机数流做异或。
// 种子是视频信息里的 decodeKey。算法和开源项目 WechatSphDecrypt / wx_channels_download 一致。

import (
	"encoding/binary"
	"io"
)

const encryptedLen = 131072

type isaac64 struct {
	cnt        int
	rsl, mm    [256]uint64
	aa, bb, cc uint64
}

func newIsaac64(seed uint64) *isaac64 {
	r := &isaac64{cnt: 255}
	r.rsl[0] = seed
	const golden = uint64(0x9e3779b97f4a7c13)
	v := [8]uint64{golden, golden, golden, golden, golden, golden, golden, golden}
	for i := 0; i < 4; i++ {
		mix(&v)
	}
	for pass := 0; pass < 2; pass++ {
		src := &r.rsl
		if pass == 1 {
			src = &r.mm
		}
		for i := 0; i < 256; i += 8 {
			for j := 0; j < 8; j++ {
				v[j] += src[i+j]
			}
			mix(&v)
			copy(r.mm[i:i+8], v[:])
		}
	}
	r.generate()
	return r
}

func (r *isaac64) generate() {
	r.cc++
	r.bb += r.cc
	for i := 0; i < 256; i++ {
		switch i % 4 {
		case 0:
			r.aa = ^(r.aa ^ (r.aa << 21))
		case 1:
			r.aa ^= r.aa >> 5
		case 2:
			r.aa ^= r.aa << 12
		case 3:
			r.aa ^= r.aa >> 33
		}
		r.aa += r.mm[(i+128)%256]
		x := r.mm[i]
		y := r.mm[(x>>3)%256] + r.aa + r.bb
		r.mm[i] = y
		r.bb = r.mm[(y>>11)%256] + x
		r.rsl[i] = r.bb
	}
}

// next 按 255→0 的顺序吐出随机数，用完再生成下一批。
func (r *isaac64) next() uint64 {
	v := r.rsl[r.cnt]
	if r.cnt == 0 {
		r.generate()
		r.cnt = 255
	} else {
		r.cnt--
	}
	return v
}

func mix(v *[8]uint64) {
	a, b, c, d, e, f, g, h := v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]
	a -= e
	f ^= h >> 9
	h += a
	b -= f
	g ^= a << 9
	a += b
	c -= g
	h ^= b >> 23
	b += c
	d -= h
	a ^= c << 15
	c += d
	e -= a
	b ^= d >> 14
	d += e
	f -= b
	c ^= e << 20
	e += f
	g -= c
	d ^= f >> 17
	f += g
	h -= d
	e ^= g << 14
	g += h
	*v = [8]uint64{a, b, c, d, e, f, g, h}
}

// keystream 生成前 n 字节的异或密钥。
func keystream(seed uint64, n int) []byte {
	r := newIsaac64(seed)
	out := make([]byte, (n+7)/8*8)
	for i := 0; i < len(out); i += 8 {
		binary.BigEndian.PutUint64(out[i:], r.next())
	}
	return out[:n]
}

// decryptReader 边读边解密：只处理文件开头 encryptedLen 字节。
type decryptReader struct {
	src io.Reader
	ks  []byte
	pos int
}

func newDecryptReader(src io.Reader, seed uint64) io.Reader {
	if seed == 0 {
		return src
	}
	return &decryptReader{src: src, ks: keystream(seed, encryptedLen)}
}

// newDecryptReaderAt 从文件第 offset 字节开始解密（预览时拖动进度条用）。
func newDecryptReaderAt(src io.Reader, seed uint64, offset int64) io.Reader {
	if seed == 0 || offset >= encryptedLen {
		return src
	}
	return &decryptReader{src: src, ks: keystream(seed, encryptedLen), pos: int(offset)}
}

func (d *decryptReader) Read(p []byte) (int, error) {
	n, err := d.src.Read(p)
	for i := 0; i < n && d.pos < len(d.ks); i++ {
		p[i] ^= d.ks[d.pos]
		d.pos++
	}
	return n, err
}
