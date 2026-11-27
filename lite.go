package main

// 【自带播放器模式】微信 4.2 起（按账号灰度），左侧“视频号”整个改成原生 liteapp：没有网页可注入，
// 视频信息（标题、decodeKey）走微信私有协议，不经过代理。代理只能看到播放器去 finder.video.qq.com 拉视频文件。
// 办法（参考 laoyu20032003/wx_video_downloader）：
//  1. 代理记下播放器拉的视频地址（liteMedia）；
//  2. 在微信进程内存里找 decodeKey 候选（"decodeKey":"123"、kvReport 串、地址附近的数字）；
//  3. 每个候选用“解密后开头 4~8 字节必须是 ftyp”验证，对上就是这条视频的钥匙（错配概率 2^-56）；
//  4. 找不到钥匙时退一步：内存里找已解密的视频开头（ftyp+moov），按文件结构正好对上文件大小来认，
//     钥匙流 = 密文开头 XOR 明文开头。
// 只在 Windows 启用（memscan_windows.go），Mac 微信仍是网页。

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

var (
	liteEnabled = runtime.GOOS == "windows"
	memScan     = scanWeChatMemory
	liteScanGap = 4 * time.Second
)

type liteMedia struct {
	ek, m       string
	url         string
	count       int
	first, last time.Time
	total       int64
	head        []byte // 密文前 16 字节
	headErr     int
	scans       int
	headerTries int
	state       string // pending / done / failed
	played      bool
	itemKey     string
	waitKey     string // 正在找钥匙时列表里先放的占位条目
}

// liteWaitLocked 开始播放就先在列表里放一条“正在找钥匙”，不用干等内存扫描（几秒）才出现。
func (s *Store) liteWaitLocked(lm *liteMedia) {
	if lm.waitKey != "" || lm.state != "pending" || !lm.played {
		return
	}
	lm.waitKey = "lite-wait:" + shortEK(lm.ek)
	now := time.Now()
	it := &Item{Key: lm.waitKey, FeedInfo: FeedInfo{ID: lm.waitKey, Title: "视频号视频（正在从微信里找解密钥匙，播放几秒就好）", Size: lm.total},
		Source: "lite", Status: "resolving", Seen: now, Opened: now}
	s.items[lm.waitKey] = it
	s.currentKey = lm.waitKey
}

// liteWaitDoneLocked 找到钥匙后把占位条目换成真的（保持原来的位置）；找不到就把占位改成失败说明。
func (s *Store) liteWaitDoneLocked(lm *liteMedia, real *Item, failMsg string) {
	ph := s.items[lm.waitKey]
	if lm.waitKey == "" || ph == nil {
		return
	}
	if real != nil {
		delete(s.items, lm.waitKey)
		real.Seen = ph.Seen
		if real.Opened.Before(ph.Opened) {
			real.Opened = ph.Opened
		}
		if s.currentKey == lm.waitKey {
			s.currentKey = real.Key
		}
		return
	}
	ph.Status, ph.Err = "error", failMsg
	ph.Title = "视频号视频（没找到解密钥匙）"
}

// OnLiteMedia 代理看到 *.video.qq.com 的视频响应时调用。
// 返回 wantHead=true 表示还缺这条视频的密文开头：代理会在转发时顺手截下前 16 字节（SetLiteHead），不用另外去下载。
func (s *Store) OnLiteMedia(raw string, ct string, h http.Header, status int) (ek string, wantHead bool) {
	if !liteEnabled || status >= 400 {
		return
	}
	ct = strings.ToLower(ct)
	if !strings.Contains(ct, "video") && h.Get("X-enclen") == "" && h.Get("X-encflag") == "" {
		return // 封面、头像也走 stodownload
	}
	pu, err := url.Parse(raw)
	if err != nil {
		return
	}
	q := pu.Query()
	ek = q.Get("encfilekey")
	if ek == "" {
		return
	}
	var total int64
	if cr := h.Get("Content-Range"); cr != "" {
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			total, _ = strconv.ParseInt(cr[i+1:], 10, 64)
		}
	} else if status == 200 {
		total, _ = strconv.ParseInt(h.Get("Content-Length"), 10, 64)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.pageSeen) < 30*time.Second {
		return "", false // 网页模式正常，视频信息由页面上报
	}
	for _, it := range s.items {
		if it.Source != "lite" && it.fileKey == ek {
			return "", false
		}
	}
	if s.lite == nil {
		s.lite = map[string]*liteMedia{}
		s.liteKick = make(chan struct{}, 1)
		go s.liteLoop()
		logf("微信用的是自带播放器：正在从微信内存里找解密钥匙。请把要下载的视频播放 5~10 秒")
	}
	s.liteSeen = time.Now()
	lm := s.lite[ek]
	if lm == nil {
		lm = &liteMedia{ek: ek, url: raw, first: time.Now(), state: "pending"}
		s.lite[ek] = lm
		tracef("自带播放器视频：%s", trimURL(raw))
	}
	lm.count++
	lm.last = time.Now()
	if m := q.Get("m"); m != "" || lm.m == "" {
		lm.url, lm.m = raw, m
	}
	if total > 0 {
		lm.total = total
	}
	wasPlayed := lm.played
	lm.played = lm.played || lm.m != "" || lm.count >= 3
	s.liteWaitLocked(lm)
	if lm.state == "done" {
		if it := s.items[lm.itemKey]; it != nil {
			if it.Status == "seen" || it.Status == "error" || it.Status == "done" {
				it.URL = lm.url // token 会过期，用最新的
				it.fileKey = ek
			}
			if lm.played && !wasPlayed {
				s.markLiteCurrentLocked(it)
			}
		}
		return ek, false
	}
	select {
	case s.liteKick <- struct{}{}:
	default:
	}
	return ek, lm.head == nil
}

// SetLiteHead 代理转发视频时截下的密文开头。
func (s *Store) SetLiteHead(ek string, head []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lm := s.lite[ek]; lm != nil && lm.head == nil && len(head) >= 16 {
		lm.head = append([]byte(nil), head[:16]...)
		select {
		case s.liteKick <- struct{}{}:
		default:
		}
	}
}

// headTap 边转发边截下响应最开头的 16 字节，不影响微信播放。
type headTap struct {
	r    io.ReadCloser
	buf  []byte
	done bool
	fn   func([]byte)
}

func (t *headTap) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if !t.done && n > 0 {
		need := 16 - len(t.buf)
		if need > n {
			need = n
		}
		t.buf = append(t.buf, p[:need]...)
		if len(t.buf) >= 16 {
			t.done = true
			t.fn(t.buf)
		}
	}
	return n, err
}

func (t *headTap) Close() error { return t.r.Close() }

// startsAtZero 这个响应是不是从文件第 0 字节开始的。
func startsAtZero(status int, h http.Header) bool {
	if status == 200 {
		return true
	}
	return status == 206 && strings.HasPrefix(strings.TrimSpace(h.Get("Content-Range")), "bytes 0-")
}

func (s *Store) markLiteCurrentLocked(it *Item) {
	it.Opened = time.Now()
	s.currentKey = it.Key
	if s.AutoDownload && it.Status != "done" {
		s.enqueueLocked(it, false)
	}
}

type liteJob struct {
	lm              *liteMedia
	ek, url         string
	head            []byte
	total           int64
	played          bool
	scans, hdrTries int
	last            time.Time
}

func (s *Store) liteLoop() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var lastScan time.Time
	scanNo := 0
	for {
		select {
		case <-s.liteKick:
		case <-tick.C:
		}
		jobs := s.litePending()
		if len(jobs) == 0 {
			continue
		}
		// 1. 密文开头：一般代理转发时已经截到了；没截到的，挑最要紧的几条自己去读（并行，最多等 10 秒）
		sortJobs(jobs)
		var need []*liteJob
		for _, j := range jobs {
			if j.head == nil && len(need) < 4 {
				need = append(need, j)
			}
		}
		var wg sync.WaitGroup
		for _, j := range need {
			wg.Add(1)
			go func(j *liteJob) {
				defer wg.Done()
				head, total, err := fetchRange(j.url, 0, 15)
				s.mu.Lock()
				defer s.mu.Unlock()
				if err != nil || len(head) < 16 {
					j.lm.headErr++
					tracef("读视频开头失败（%s）：%v", shortEK(j.ek), err)
					if j.lm.headErr >= 3 {
						j.lm.state = "failed"
						s.liteWaitDoneLocked(j.lm, nil, "视频地址打不开，在微信里重新播放一下")
						if j.played {
							logf("视频地址打不开：%v", err)
						}
					}
					return
				}
				if j.lm.head == nil {
					j.lm.head = head
				}
				j.head = j.lm.head
				if total > 0 {
					j.lm.total, j.total = total, total
				}
			}(j)
		}
		wg.Wait()
		var ready []*liteJob
		for _, j := range jobs {
			if j.head == nil {
				continue
			}
			if isPlainMP4(j.head) { // 没加密
				s.liteResolved(j.lm, 0, nil, seedInfo{})
				continue
			}
			ready = append(ready, j)
		}
		if len(ready) == 0 || time.Since(lastScan) < liteScanGap {
			continue
		}
		lastScan = time.Now()
		scanNo++

		// 2. 内存里找钥匙
		var prefixes []string
		for _, j := range ready { // ready 已按要紧程度排好；每多一条就要多搜一遍内存，只取前 6 条
			if p := ekPrefix(j.ek); p != "" && len(prefixes) < 6 {
				prefixes = append(prefixes, p)
			}
		}
		seeds := seedSet{}
		start := time.Now()
		procs, err := memScan(scanNo%2 == 0, func(buf []byte) { extractSeeds(buf, prefixes, seeds) })
		took := time.Since(start).Round(100 * time.Millisecond)
		tracef("内存扫描 #%d：进程 %d，候选钥匙 %d，待对视频 %d，用时 %v，err=%v", scanNo, procs, len(seeds), len(ready), took, err)
		if err != nil {
			if scanNo <= 2 || scanNo%10 == 0 {
				logf("读取微信内存失败：%v", err)
			}
		} else if scanNo <= 3 || scanNo%10 == 0 {
			logf("扫描微信内存：%d 个进程，候选钥匙 %d 个，用时 %v", procs, len(seeds), took)
		}
		var left []*liteJob
		for _, j := range ready {
			if seed, info, ok := seeds.match(j.head, ekPrefix(j.ek)); ok {
				s.liteResolved(j.lm, seed, nil, info)
			} else {
				left = append(left, j)
			}
		}

		// 3. 找不到钥匙的、正在看的视频：退一步找内存里解密好的开头
		var tryHdr []*liteJob
		s.mu.Lock()
		for _, j := range left {
			j.lm.scans++
			j.scans = j.lm.scans
			if j.played && j.scans >= 2 && j.hdrTries < 3 && j.total > encryptedLen {
				j.lm.headerTries++
				tryHdr = append(tryHdr, j)
			} else if j.scans >= 8 {
				j.lm.state = "failed"
				s.liteWaitDoneLocked(j.lm, nil, "没找到解密钥匙，在微信里把它多播放几秒再试")
				if j.played {
					logf("这个视频没找到解密钥匙（%s）。可以在微信里把它多播放几秒再试", shortEK(j.ek))
				}
			}
		}
		s.mu.Unlock()
		if len(tryHdr) > 0 {
			var hdrs [][]byte
			seen := map[[16]byte]bool{}
			memScan(false, func(buf []byte) { findPlainHeaders(buf, &hdrs, seen) })
			tracef("明文开头候选 %d 个", len(hdrs))
			logf("没找到钥匙，改找微信解密好的视频开头：候选 %d 个", len(hdrs))
			for _, j := range tryHdr {
				if ks := matchHeader(j.url, j.total, hdrs); ks != nil {
					s.liteResolved(j.lm, 0, ks, seedInfo{})
				}
			}
		}
	}
}

// sortJobs 正在看的排前面，其次是正片地址（20302），再按最近。
func sortJobs(jobs []*liteJob) {
	rank := func(j *liteJob) int {
		r := 0
		if j.played {
			r += 2
		}
		if strings.Contains(j.url, "/20302/") {
			r++
		}
		return r
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		ra, rb := rank(jobs[a]), rank(jobs[b])
		if ra != rb {
			return ra > rb
		}
		return jobs[a].last.After(jobs[b].last)
	})
}

func (s *Store) litePending() []*liteJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*liteJob
	for ek, lm := range s.lite {
		if time.Since(lm.last) > 15*time.Minute {
			delete(s.lite, ek)
			continue
		}
		if lm.state != "pending" {
			continue
		}
		out = append(out, &liteJob{lm: lm, ek: lm.ek, url: lm.url, head: lm.head, total: lm.total, played: lm.played, scans: lm.scans, hdrTries: lm.headerTries, last: lm.last})
	}
	return out
}

// liteResolved 找到钥匙（seed）或钥匙流（ks），放进列表。
func (s *Store) liteResolved(lm *liteMedia, seed uint64, ks []byte, info seedInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lm.state != "pending" {
		return
	}
	id := "lite:" + strconv.FormatUint(seed, 10)
	if ks != nil {
		h := md5.Sum(ks)
		id = "lite:h" + hex.EncodeToString(h[:6])
	} else if seed == 0 {
		h := md5.Sum([]byte(lm.ek))
		id = "lite:p" + hex.EncodeToString(h[:6])
	}
	lm.state, lm.itemKey = "done", id
	it, ok := s.items[id]
	if !ok {
		title := info.title
		if title == "" {
			title = "视频号视频 " + time.Now().Format("01-02 15:04:05")
		}
		f := FeedInfo{ID: id, Title: title, Nickname: info.nick, URL: lm.url, Size: lm.total}
		if seed != 0 {
			f.DecodeKey = strconv.FormatUint(seed, 10)
		}
		it = &Item{FeedInfo: f, Key: id, Source: "lite", Status: "seen", Seen: time.Now(), fileKey: lm.ek, ks: ks}
		if file := s.downloadedLocked(id); file != "" {
			it.Status, it.File = "done", file
		}
		s.items[id] = it
		how := "钥匙"
		if ks != nil {
			how = "解密好的开头"
		}
		logf("发现视频（微信自带播放器，内存里找到%s）：%s", how, label(it))
	} else if it.Status == "seen" || it.Status == "error" || it.Status == "done" {
		it.URL, it.fileKey = lm.url, lm.ek
		if ks != nil {
			it.ks = ks
		}
	}
	s.liteWaitDoneLocked(lm, it, "")
	if lm.played {
		s.markLiteCurrentLocked(it)
	}
}

// ---------- 钥匙候选 ----------

type seedInfo struct {
	title, nick string
	eks         map[string]bool // 窗口里出现过的 encfilekey 前缀：对得上才用标题
}

type seedSet map[uint64]*seedInfo

func (ss seedSet) add(v string, title, nick string, ekHits []string) {
	if len(ss) > 50000 {
		return
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
		return
	}
	si := ss[n]
	if si == nil {
		si = &seedInfo{eks: map[string]bool{}}
		ss[n] = si
	}
	for _, e := range ekHits {
		si.eks[e] = true
		if title != "" && si.title == "" {
			si.title = title
		}
		if nick != "" && si.nick == "" {
			si.nick = nick
		}
	}
}

func (ss seedSet) match(head []byte, prefix string) (uint64, seedInfo, bool) {
	if len(head) < 8 {
		return 0, seedInfo{}, false
	}
	for seed, si := range ss {
		if seedMatches(seed, head) {
			info := seedInfo{}
			if prefix != "" && si.eks[prefix] {
				info.title, info.nick = si.title, si.nick
			}
			return seed, info, true
		}
	}
	return 0, seedInfo{}, false
}

func seedMatches(seed uint64, head []byte) bool {
	r := newIsaac64(seed)
	var ks [8]byte
	binary.BigEndian.PutUint64(ks[:], r.next())
	var p [8]byte
	for i := range p {
		p[i] = head[i] ^ ks[i]
	}
	return isPlainMP4(p[:])
}

func isPlainMP4(b []byte) bool {
	return len(b) >= 8 && b[0] == 0 && b[1] == 0 && string(b[4:8]) == "ftyp"
}

func ekPrefix(ek string) string {
	var b strings.Builder
	for _, c := range ek {
		if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b.WriteRune(c)
			if b.Len() == 24 {
				return b.String()
			}
		} else {
			break
		}
	}
	if b.Len() >= 16 {
		return b.String()
	}
	return ""
}

func shortEK(ek string) string {
	if len(ek) > 12 {
		return ek[:12] + "…"
	}
	return ek
}

var (
	reDecodeKey = regexp.MustCompile(`^(?:decodeKey|decode_key)\\{0,3}"?\s*[:=]\s*\\{0,3}"?(\d{1,20})`)
	reReport    = regexp.MustCompile(`^stodownload[^,\s"]*,(\d{1,20}),`)
	reDigits    = regexp.MustCompile(`\d{6,20}`)
	reDesc      = regexp.MustCompile(`"description"\s*:\s*("(?:[^"\\]|\\.){1,400}")`)
	reNick      = regexp.MustCompile(`"nickname"\s*:\s*("(?:[^"\\]|\\.){1,100}")`)
)

func u16(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, c := range utf16.Encode([]rune(s)) {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func decodeU16(b []byte) string {
	n := len(b) / 2
	w := make([]uint16, n)
	for i := 0; i < n; i++ {
		w[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(w))
}

// window 取 i 前后的文字，返回 (i 之前, i 开始之后)。wide=true 按 UTF-16 解码（V8 里有中文的字符串是双字节存的）。
func window(buf []byte, i, before, after int, wide bool) (string, string) {
	a, b := i-before, i+after
	if wide {
		a, b = i-before*2, i+after*2
		if a < 0 {
			a = i % 2
		}
	} else if a < 0 {
		a = 0
	}
	if b > len(buf) {
		b = len(buf)
	}
	if wide {
		return decodeU16(buf[a:i]), decodeU16(buf[i:b])
	}
	return string(buf[a:i]), string(buf[i:b])
}

func nearestJSON(re *regexp.Regexp, text string, pos int) string {
	best, bestD := "", 1<<30
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		d := m[0] - pos
		if d < 0 {
			d = -d
		}
		if d < bestD {
			var s string
			if json.Unmarshal([]byte(text[m[2]:m[3]]), &s) == nil && strings.TrimSpace(s) != "" {
				best, bestD = s, d
			}
		}
	}
	return best
}

func eachIndex(buf, needle []byte, limit int, fn func(i int)) {
	off, n := 0, 0
	for n < limit {
		j := bytes.Index(buf[off:], needle)
		if j < 0 {
			return
		}
		fn(off + j)
		off += j + len(needle)
		n++
	}
}

// extractSeeds 从一块内存里找 decodeKey 候选（宽松收集，靠 seedMatches 精确验证）。
func extractSeeds(buf []byte, prefixes []string, ss seedSet) {
	for _, wide := range []bool{false, true} {
		enc := func(s string) []byte {
			if wide {
				return u16(s)
			}
			return []byte(s)
		}
		hits := func(text string) []string {
			var out []string
			for _, p := range prefixes {
				if strings.Contains(text, p) {
					out = append(out, p)
				}
			}
			return out
		}
		// "decodeKey":"123"
		for _, key := range []string{"decodeKey", "decode_key"} {
			eachIndex(buf, enc(key), 5000, func(i int) {
				pre, post := window(buf, i, 6000, 6000, wide)
				m := reDecodeKey.FindStringSubmatch(post)
				if m == nil {
					return
				}
				text := pre + post
				ss.add(m[1], nearestJSON(reDesc, text, len(pre)), nearestJSON(reNick, text, len(pre)), hits(text))
			})
		}
		// kvReport：oid,https://…stodownload?…,decodeKey,
		eachIndex(buf, enc("stodownload"), 5000, func(i int) {
			_, post := window(buf, i, 0, 3000, wide)
			if m := reReport.FindStringSubmatch(post); m != nil {
				ss.add(m[1], "", "", nil)
			}
		})
		// 这条视频地址附近的所有数字
		for _, p := range prefixes {
			eachIndex(buf, enc(p), 200, func(i int) {
				pre, post := window(buf, i, 2500, 2500, wide)
				text := pre + post
				title, nick := nearestJSON(reDesc, text, len(pre)), nearestJSON(reNick, text, len(pre))
				for _, d := range reDigits.FindAllString(text, 400) {
					ss.add(d, title, nick, []string{p})
				}
			})
		}
	}
}

// ---------- 退路：内存里解密好的开头 ----------

func be32(b []byte, o int) uint32 { return binary.BigEndian.Uint32(b[o:]) }

// moovFollows ftyp 之后最多跨 3 个空 box 撞上 moov。
func moovFollows(b []byte, p int) bool {
	for hop := 0; hop < 4; hop++ {
		if p+8 > len(b) {
			return false
		}
		sz := be32(b, p)
		if sz < 8 || sz > 32<<20 {
			return false
		}
		switch string(b[p+4 : p+8]) {
		case "moov":
			return true
		case "free", "wide", "uuid", "skip":
		default:
			return false
		}
		p += int(sz)
	}
	return false
}

func findPlainHeaders(buf []byte, out *[][]byte, seen map[[16]byte]bool) {
	eachIndex(buf, []byte("ftyp"), 1<<20, func(i int) {
		p := i - 4
		if p < 0 || p+encryptedLen > len(buf) || len(*out) >= 200 {
			return
		}
		s1 := be32(buf, p)
		if s1 < 16 || s1 > 512 || !moovFollows(buf, p+int(s1)) {
			return
		}
		h := md5.Sum(buf[p : p+encryptedLen])
		if seen[h] {
			return
		}
		seen[h] = true
		*out = append(*out, append([]byte(nil), buf[p:p+encryptedLen]...))
	})
}

// matchHeader 在候选明文开头里挑出属于这条视频的：顶层 box 一路接到正好是文件末尾。
// 对上了就返回钥匙流（密文开头 XOR 明文开头）。
func matchHeader(u string, total int64, hdrs [][]byte) []byte {
	for _, h := range hdrs {
		if headerFits(u, total, h) {
			c, _, err := fetchRange(u, 0, encryptedLen-1)
			if err != nil || len(c) < encryptedLen {
				return nil
			}
			ks := make([]byte, encryptedLen)
			for i := range ks {
				ks[i] = c[i] ^ h[i]
			}
			return ks
		}
	}
	return nil
}

func headerFits(u string, total int64, h []byte) bool {
	var off int64
	moov, mdat := false, false
	for n := 0; n < 16 && off < total; n++ {
		var hb []byte
		if off+16 <= int64(len(h)) {
			hb = h[off : off+16]
		} else if off >= int64(len(h)) {
			b, _, err := fetchRange(u, off, off+15) // 明文区，直接从服务器读
			if err != nil || len(b) < 8 {
				return false
			}
			hb = b
		} else {
			return false // box 头跨在加密边界上，少见，放弃
		}
		size := int64(be32(hb, 0))
		typ := string(hb[4:8])
		if n == 0 && typ != "ftyp" {
			return false
		}
		if size == 1 && len(hb) >= 16 {
			size = int64(binary.BigEndian.Uint64(hb[8:16]))
		} else if size == 0 {
			size = total - off
		}
		if size < 8 {
			return false
		}
		switch typ {
		case "moov":
			moov = true
		case "mdat":
			mdat = true
		}
		off += size
	}
	return moov && mdat && off == total
}

// fetchRange 读视频文件的一段（闭区间），顺便从 Content-Range 拿文件总大小。
func fetchRange(u string, a, b int64) ([]byte, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", a, b))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var total int64
	switch resp.StatusCode {
	case 206:
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 {
				total, _ = strconv.ParseInt(cr[i+1:], 10, 64)
			}
		}
	case 200:
		if a != 0 {
			return nil, 0, fmt.Errorf("服务器不支持分段读取")
		}
		total = resp.ContentLength
	default:
		return nil, 0, fmt.Errorf("服务器返回 %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, b-a+1))
	return data, total, err
}
