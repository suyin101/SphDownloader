package main

import "encoding/binary"

// 参考实现（按 WechatSphDecrypt 的写法逐行抄写），只用于测试对比
type refCtx struct {
	cnt        uint64
	seed, mm   [256]uint64
	aa, bb, cc uint64
}

func refMix(a, b, c, d, e, f, g, h *uint64) {
	*a -= *e
	*f ^= *h >> 9
	*h += *a
	*b -= *f
	*g ^= *a << 9
	*a += *b
	*c -= *g
	*h ^= *b >> 23
	*b += *c
	*d -= *h
	*a ^= *c << 15
	*c += *d
	*e -= *a
	*b ^= *d >> 14
	*d += *e
	*f -= *b
	*c ^= *e << 20
	*e += *f
	*g -= *c
	*d ^= *f >> 17
	*f += *g
	*h -= *d
	*e ^= *g << 14
	*g += *h
}

func (x *refCtx) gen() {
	x.cc++
	x.bb += x.cc
	for i := 0; i < 256; i++ {
		switch i % 4 {
		case 0:
			x.aa = ^(x.aa ^ (x.aa << 21))
		case 1:
			x.aa ^= x.aa >> 5
		case 2:
			x.aa ^= x.aa << 12
		case 3:
			x.aa ^= x.aa >> 33
		}
		x.aa += x.mm[(i+128)%256]
		v := x.mm[i]
		y := x.mm[(v>>3)%256] + x.aa + x.bb
		x.mm[i] = y
		x.bb = x.mm[(y>>11)%256] + v
		x.seed[i] = x.bb
	}
}

func refNew(key uint64) *refCtx {
	x := &refCtx{cnt: 255}
	g := uint64(0x9e3779b97f4a7c13)
	a, b, c, d, e, f, gg, h := g, g, g, g, g, g, g, g
	x.seed[0] = key
	for i := 0; i < 4; i++ {
		refMix(&a, &b, &c, &d, &e, &f, &gg, &h)
	}
	for i := 0; i < 256; i += 8 {
		a += x.seed[i]
		b += x.seed[i+1]
		c += x.seed[i+2]
		d += x.seed[i+3]
		e += x.seed[i+4]
		f += x.seed[i+5]
		gg += x.seed[i+6]
		h += x.seed[i+7]
		refMix(&a, &b, &c, &d, &e, &f, &gg, &h)
		x.mm[i], x.mm[i+1], x.mm[i+2], x.mm[i+3] = a, b, c, d
		x.mm[i+4], x.mm[i+5], x.mm[i+6], x.mm[i+7] = e, f, gg, h
	}
	for i := 0; i < 256; i += 8 {
		a += x.mm[i]
		b += x.mm[i+1]
		c += x.mm[i+2]
		d += x.mm[i+3]
		e += x.mm[i+4]
		f += x.mm[i+5]
		gg += x.mm[i+6]
		h += x.mm[i+7]
		refMix(&a, &b, &c, &d, &e, &f, &gg, &h)
		x.mm[i], x.mm[i+1], x.mm[i+2], x.mm[i+3] = a, b, c, d
		x.mm[i+4], x.mm[i+5], x.mm[i+6], x.mm[i+7] = e, f, gg, h
	}
	x.gen()
	return x
}

func (x *refCtx) rnd() uint64 {
	r := x.seed[x.cnt]
	if x.cnt == 0 {
		x.gen()
		x.cnt = 255
	} else {
		x.cnt--
	}
	return r
}

func refDecryptData(data []byte, encLen uint32, key uint64) {
	x := refNew(key)
	for i := uint32(0); i < encLen; i += 8 {
		var t [8]byte
		binary.BigEndian.PutUint64(t[:], x.rnd())
		for j := uint32(0); j < 8 && i+j < encLen; j++ {
			data[i+j] ^= t[j]
		}
	}
}
