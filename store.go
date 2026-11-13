package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FeedInfo struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Nickname   string `json:"nickname"`
	URL        string `json:"url"`
	DecodeKey  string `json:"decodeKey"`
	Cover      string `json:"cover"`
	Duration   int    `json:"duration"`
	Size       int64  `json:"size"`
	CreateTime int64  `json:"createtime"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// Item 列表里的一条。状态：
// resolving 解析链接中 / waiting 等微信打开 / seen 未下载 / queued 排队 / downloading 下载中 / done 完成 / error 失败
type Item struct {
	FeedInfo
	Key       string    `json:"key"`
	Source    string    `json:"source"` // sniff 嗅探 / link 链接
	Link      string    `json:"link"`
	LinkKey   string    `json:"linkKey"`
	Status    string    `json:"status"`
	Progress  int64     `json:"progress"`
	Total     int64     `json:"total"`
	File      string    `json:"file"`
	CoverFile string    `json:"coverFile"`
	Err       string    `json:"err"`
	Seen      time.Time `json:"seen"`   // 第一次出现在列表的时间（之后不变，排序稳定）
	Opened    time.Time `json:"opened"` // 你在微信里打开它 / 粘贴链接的时间；打开过的排在最上面
	fileKey   string
}

type linkTask struct {
	itemKey  string
	exportID string
	feedURL  string
	state    string // pending / opening / done
	openedAt time.Time
	tries    int
}

type Store struct {
	mu            sync.Mutex
	items         map[string]*Item
	tasks         []*linkTask
	AutoDownload  bool
	DownloadCover bool
	Dir           string
	gotCurrent    bool      // 收到过“正在看的视频”信号；没有的话才用视频请求来兜底
	pageSeen      time.Time // 页面最近一次来取任务（新脚本才会）
	inPlace       bool      // 链接下载改为在当前网页里跳转（部分 Windows 微信会把新窗口改走自带播放器，见 CLAUDE.md 灰度坑）
	lastReport    time.Time // 页面最近一次上报视频（旧脚本也会）
	queue         chan *Item
	currentKey    string // 最近打开的那条（界面上标“正在看”）
	history       map[string]histEntry
	histPath      string
}

func NewStore(dir string) *Store {
	s := &Store{items: map[string]*Item{}, AutoDownload: false, Dir: dir, queue: make(chan *Item, 2000), history: map[string]histEntry{}}
	for i := 0; i < 3; i++ {
		go s.worker()
	}
	return s
}

func encFileKey(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return pu.Query().Get("encfilekey")
}

func (s *Store) MarkPageSeen() {
	s.mu.Lock()
	s.pageSeen = time.Now()
	s.mu.Unlock()
}

// PageState 视频号页面连接状态：online 正常 / stale 页面开着但脚本是旧的，要重开视频号窗口 / offline 没打开。
func (s *Store) PageState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.pageSeen) < 10*time.Second {
		return "online"
	}
	if !s.lastReport.IsZero() && time.Since(s.lastReport) < 10*time.Minute {
		return "stale"
	}
	return "offline"
}

// AddReport 处理页面上报。href 是上报页面的地址，用来对上“链接下载”的任务。
func (s *Store) AddReport(kind string, feeds []FeedInfo, href string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReport = time.Now()
	current := kind == "current"
	if current {
		s.gotCurrent = true
	}
	if t := s.matchTaskLocked(kind, feeds, href); t != nil {
		return
	}
	s.addFeedsLocked(feeds, current, false, "sniff")
}

func (s *Store) addFeedsLocked(feeds []FeedInfo, current, force bool, source string) []*Item {
	var out []*Item
	for _, f := range feeds {
		if f.ID == "" || f.URL == "" {
			continue
		}
		it, ok := s.items[f.ID]
		if !ok {
			it = &Item{FeedInfo: f, Key: f.ID, Source: source, Status: "seen", Seen: time.Now(), fileKey: encFileKey(f.URL)}
			if file := s.downloadedLocked(f.ID); file != "" { // 以前下载过
				it.Status, it.File = "done", file
			}
			s.items[f.ID] = it
		} else if it.Status == "seen" || it.Status == "error" || it.Status == "done" {
			// 地址里的 token 会过期，用最新的；标题封面等保留已有的
			keep := it.FeedInfo
			it.FeedInfo = f
			if it.Title == "" {
				it.Title = keep.Title
			}
			if it.Cover == "" {
				it.Cover = keep.Cover
			}
			it.fileKey = encFileKey(f.URL)
		}
		if current {
			it.Opened = time.Now()
			s.currentKey = it.Key
		}
		// 自动触发（包括链接下载）不会重复下载已下载的；要重下得手动点“重新下载”
		if (force || (current && s.AutoDownload)) && it.Status != "done" {
			s.enqueueLocked(it, false)
		}
		out = append(out, it)
	}
	return out
}

// matchTaskLocked 页面打开的是“链接下载”任务里的视频时，把视频信息对上任务并开始下载。
func (s *Store) matchTaskLocked(kind string, feeds []FeedInfo, href string) *linkTask {
	var opening *linkTask
	for _, t := range s.tasks {
		if t.state == "opening" {
			opening = t
			break
		}
	}
	if opening == nil || len(feeds) == 0 {
		return nil
	}
	u, err := url.Parse(href)
	if err != nil || !strings.HasSuffix(u.Path, "/web/pages/feed") {
		return nil
	}
	eid := u.Query().Get("eid")
	if eid != "" && eid != opening.exportID {
		return nil
	}
	var target *FeedInfo
	if kind == "current" {
		target = &feeds[0]
	} else if len(feeds) == 1 {
		target = &feeds[0]
	}
	if target == nil || target.URL == "" {
		return nil
	}
	opening.state = "done"
	ph := s.items[opening.itemKey]
	delete(s.items, opening.itemKey)
	f := *target
	if ph != nil {
		if f.Title == "" {
			f.Title = ph.Title
		}
		if f.Nickname == "" {
			f.Nickname = ph.Nickname
		}
		if f.Cover == "" {
			f.Cover = ph.Cover
		}
	}
	items := s.addFeedsLocked([]FeedInfo{f}, true, true, "link")
	if len(items) > 0 && ph != nil {
		items[0].Seen = ph.Seen
		items[0].Opened = ph.Opened // 链接下载保持粘贴时的顺序
		items[0].Link = ph.Link
		items[0].LinkKey = ph.LinkKey
		items[0].Source = "link"
	}
	if len(items) > 0 {
		if items[0].Status == "done" {
			logf("这个视频之前下载过，不再重复下载：%s", label(items[0]))
		} else {
			logf("链接已解析，开始下载：%s", label(items[0]))
		}
	}
	// 页面里同时带出来的其它视频（推荐列表）照常进列表
	if len(feeds) > 1 {
		s.addFeedsLocked(feeds[1:], false, false, "sniff")
	}
	return opening
}

// AddLinks 解析一段文字里的视频号链接，加入列表并排队等微信打开。
func (s *Store) AddLinks(text string) int {
	links := parseLinks(text)
	s.mu.Lock()
	var fresh []linkRef
	base := time.Now()
	for i, l := range links {
		opened := base.Add(time.Duration(len(links)-i)) // 一次贴多条时，第一条排最上面（相差几纳秒）
		k := "link:" + l.key()
		if it, ok := s.items[k]; ok && it.Status != "error" {
			continue
		}
		dup := false
		for _, it := range s.items {
			if it.LinkKey == l.key() && it.Key != k {
				dup = true // 列表里已经有这个链接的视频了
				it.Opened = opened
			}
		}
		if dup {
			continue
		}
		if id, h := s.downloadedLinkLocked(l.key()); id != "" { // 以前下载过：直接显示已下载
			s.items[id] = &Item{Key: id, FeedInfo: FeedInfo{ID: id, Title: h.Title, Nickname: h.Nickname, Cover: h.Cover},
				Source: "link", Link: l.Raw, LinkKey: l.key(), Status: "done", File: h.File, Seen: base, Opened: opened}
			logf("这个链接之前下载过，不再重复下载：%s", h.Title)
			continue
		}
		s.items[k] = &Item{Key: k, FeedInfo: FeedInfo{ID: k, Title: l.Raw}, Source: "link", Link: l.Raw, LinkKey: l.key(), Status: "resolving", Seen: base, Opened: opened}
		fresh = append(fresh, l)
	}
	s.mu.Unlock()
	go func() {
		for _, l := range fresh {
			info, err := resolveLink(l)
			k := "link:" + l.key()
			s.mu.Lock()
			it := s.items[k]
			if it == nil {
				s.mu.Unlock()
				continue
			}
			if err != nil {
				it.Status, it.Err = "error", err.Error()
				logf("链接解析失败 %s：%v", l.Raw, err)
				s.mu.Unlock()
				continue
			}
			it.Title, it.Nickname, it.Cover, it.CreateTime = info.Title, info.Nickname, info.Cover, info.CreateTime
			it.Status = "waiting"
			s.tasks = append(s.tasks, &linkTask{itemKey: k, exportID: info.ExportID, feedURL: info.FeedURL, state: "pending"})
			s.mu.Unlock()
		}
	}()
	return len(fresh)
}

// NextTask 页面来取下一个要打开的视频。同一时间只开一个，等它下载上了再开下一个。
// OpenInPlace 链接下载是否改为在当前网页里跳转。
func (s *Store) OpenInPlace() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inPlace
}

func (s *Store) NextTask() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageSeen = time.Now()
	for _, t := range s.tasks {
		if t.state != "opening" {
			continue
		}
		wait := 25 * time.Second
		if !s.inPlace {
			wait = 10 * time.Second // 新窗口方式正常 2 秒内就有结果
		}
		if time.Since(t.openedAt) < wait {
			return ""
		}
		if !s.inPlace {
			// 新窗口打不开视频网页，多半是被微信改走了自带播放器：以后都在当前网页里跳转，这次不算失败
			s.inPlace = true
			t.tries--
			t.state = "pending"
			logf("微信没用网页打开视频，改为在视频号页面里直接跳转")
		} else if t.tries < 2 {
			t.state = "pending"
		} else {
			t.state = "done"
			if it := s.items[t.itemKey]; it != nil {
				it.Status, it.Err = "error", "微信没有打开这条视频（视频号窗口要一直开着），点“下载”重试"
			}
		}
	}
	for _, t := range s.tasks {
		if t.state == "pending" {
			t.state, t.openedAt = "opening", time.Now()
			t.tries++
			if it := s.items[t.itemKey]; it != nil {
				it.Status = "waiting"
			}
			return t.feedURL
		}
	}
	return ""
}

// OnMediaRequest 播放器去拉视频文件时调用。只有页面钩子没生效时才用它触发自动下载。
func (s *Store) OnMediaRequest(fileKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gotCurrent || !s.AutoDownload {
		return
	}
	for _, it := range s.items {
		if it.fileKey != "" && it.fileKey == fileKey {
			s.enqueueLocked(it, false)
			return
		}
	}
}

// Enqueue 手动下载（可多条）。失败的链接任务会重新去微信里打开。
func (s *Store) Enqueue(keys []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range keys {
		it, ok := s.items[k]
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "link:") { // 还没拿到视频地址的链接
			for _, t := range s.tasks {
				if t.itemKey == k && t.state == "done" {
					t.state, t.tries = "pending", 0
					it.Status, it.Err = "waiting", ""
					n++
				}
			}
			continue
		}
		if s.enqueueLocked(it, true) {
			n++
		}
	}
	return n
}

func (s *Store) enqueueLocked(it *Item, force bool) bool {
	ok := it.Status == "seen" || it.Status == "error" || (force && it.Status == "done")
	if !ok {
		return false
	}
	it.Status = "queued"
	it.Err = ""
	select {
	case s.queue <- it:
		logf("排队下载：%s", label(it))
		return true
	default:
		it.Status = "error"
		it.Err = "队列已满"
		return false
	}
}

// Busy 正在下载/排队/等微信打开的数量。
func (s *Store) Busy() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, it := range s.items {
		switch it.Status {
		case "queued", "downloading", "waiting", "resolving":
			n++
		}
	}
	return n
}

// Clear 清空列表（正在下载的保留），未开始的链接任务也取消。
func (s *Store) Clear() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, it := range s.items {
		if it.Status == "downloading" || it.Status == "queued" {
			continue
		}
		delete(s.items, k)
		n++
	}
	for _, t := range s.tasks {
		if _, ok := s.items[t.itemKey]; !ok {
			t.state = "done"
		}
	}
	return n
}

func (s *Store) Remove(keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if it, ok := s.items[k]; ok && it.Status != "downloading" && it.Status != "queued" {
			delete(s.items, k)
			for _, t := range s.tasks {
				if t.itemKey == k {
					t.state = "done"
				}
			}
		}
	}
}

func (s *Store) Get(key string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[key]
	if !ok {
		return Item{}, false
	}
	return *it, true
}

// Snapshot 列表排序：打开过的（按打开时间，新的在上）> 只是预加载的（按出现时间，新的在上）。
func (s *Store) Snapshot() ([]Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		out = append(out, *it)
	}
	sortItems(out)
	return out, s.gotCurrent
}

func sortItems(out []Item) {
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Opened.IsZero() != b.Opened.IsZero() {
			return !a.Opened.IsZero()
		}
		if !a.Opened.Equal(b.Opened) {
			return a.Opened.After(b.Opened)
		}
		if !a.Seen.Equal(b.Seen) {
			return a.Seen.After(b.Seen)
		}
		return a.Key < b.Key
	})
}

func (s *Store) CurrentKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentKey
}

func (s *Store) worker() {
	for it := range s.queue {
		s.mu.Lock()
		if it.Status != "queued" { // 排队期间被删掉或已处理
			s.mu.Unlock()
			continue
		}
		it.Status = "downloading"
		f := it.FeedInfo
		s.mu.Unlock()
		path, err := s.download(it, f)
		s.mu.Lock()
		if err != nil {
			it.Status = "error"
			it.Err = err.Error()
			logf("下载失败：%s（%v）", label(it), err)
		} else {
			it.Status = "done"
			it.File = path
			s.recordLocked(it)
			logf("下载完成：%s", filepath.Base(path))
		}
		withCover := s.DownloadCover
		s.mu.Unlock()
		if err == nil && withCover {
			s.SaveCover(it.Key)
		}
	}
}

// SaveCover 保存封面图：和视频同名的 .jpg；没下视频时用标题命名。
func (s *Store) SaveCover(key string) (string, error) {
	s.mu.Lock()
	it, ok := s.items[key]
	if !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("找不到这条视频")
	}
	cover, file, dir, f := it.Cover, it.File, s.Dir, it.FeedInfo
	s.mu.Unlock()
	if cover == "" {
		return "", fmt.Errorf("这条视频没有封面地址")
	}
	var target string
	if file != "" {
		target = strings.TrimSuffix(file, filepath.Ext(file)) + ".jpg"
	} else {
		target = uniquePath(filepath.Join(dir, strings.TrimSuffix(fileName(f), ".mp4")+".jpg"))
	}
	os.MkdirAll(filepath.Dir(target), 0o755)
	resp, err := getWithFallback(cover)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	tmp := target + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	os.Rename(tmp, target)
	s.mu.Lock()
	it.CoverFile = target
	s.mu.Unlock()
	logf("封面已保存：%s", filepath.Base(target))
	return target, nil
}

// originalURL 只保留 encfilekey 和 token，拿到的是原画质。
func originalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	k, t := q.Get("encfilekey"), q.Get("token")
	if k == "" || t == "" {
		return raw
	}
	nq := url.Values{}
	nq.Set("encfilekey", k)
	nq.Set("token", t)
	u.RawQuery = nq.Encode()
	return u.String()
}

var client = &http.Client{
	Timeout:   60 * time.Minute,
	Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 30 * time.Second},
}

func parseSeed(k string) (uint64, error) {
	if k == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(k, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解密钥匙格式不对：%s", k)
	}
	return v, nil
}

func (s *Store) download(it *Item, f FeedInfo) (string, error) {
	seed, err := parseSeed(f.DecodeKey)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	dir := s.Dir
	s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	resp, err := getWithFallback(originalURL(f.URL), f.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	final := uniquePath(filepath.Join(dir, fileName(f)))
	tmp := final + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	it.Total = resp.ContentLength
	it.Progress = 0
	s.mu.Unlock()

	pr := &progressReader{r: newDecryptReader(resp.Body, seed), onRead: func(n int64) {
		s.mu.Lock()
		it.Progress += n
		s.mu.Unlock()
	}}
	_, err = io.Copy(out, pr)
	out.Close()
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("下载中断：%v", err)
	}
	if !looksLikeMP4(tmp) {
		os.Remove(tmp)
		return "", fmt.Errorf("解密后不是有效视频，可能是钥匙不对或格式变了")
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	return final, nil
}

// ServePreview 边下边解密地播放（支持拖动进度条），给软件里的预览窗口用。
func (s *Store) ServePreview(w http.ResponseWriter, r *http.Request, key string) {
	it, ok := s.Get(key)
	if !ok || it.URL == "" {
		http.Error(w, "这条视频还没有地址", 404)
		return
	}
	if it.Status == "done" && it.File != "" {
		if _, err := os.Stat(it.File); err == nil {
			http.ServeFile(w, r, it.File)
			return
		}
	}
	seed, err := parseSeed(it.DecodeKey)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", originalURL(it.URL), nil)
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	var offset int64
	if cr := resp.Header.Get("Content-Range"); cr != "" { // bytes 100-200/300
		if sp := strings.Fields(cr); len(sp) == 2 {
			if a, _, ok := strings.Cut(sp[1], "-"); ok {
				offset, _ = strconv.ParseInt(a, 10, 64)
			}
		}
	}
	for _, h := range []string{"Content-Length", "Content-Range", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, newDecryptReaderAt(resp.Body, seed, offset))
}

func getWithFallback(urls ...string) (*http.Response, error) {
	var lastErr error
	for i, u := range urls {
		if i > 0 && u == urls[0] {
			continue
		}
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			lastErr = fmt.Errorf("服务器返回 %d（链接可能过期了，重新打开这条视频再试）", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func looksLikeMP4(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 12)
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return string(head[4:8]) == "ftyp"
}

type progressReader struct {
	r      io.Reader
	onRead func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.onRead(int64(n))
	}
	return n, err
}

var badChars = regexp.MustCompile(`[\\/:*?"<>|\r\n\t]+|#\S+`)

func fileName(f FeedInfo) string {
	title := strings.TrimSpace(badChars.ReplaceAllString(f.Title, " "))
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = "无标题"
	}
	if r := []rune(title); len(r) > 50 {
		title = string(r[:50])
	}
	name := title
	if nick := strings.TrimSpace(badChars.ReplaceAllString(f.Nickname, "")); nick != "" {
		name = nick + "_" + title
	}
	if f.CreateTime > 0 {
		name += "_" + time.Unix(f.CreateTime, 0).Format("20060102")
	}
	return strings.TrimRight(name, ". ") + ".mp4"
}

func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
}

func label(it *Item) string {
	t := []rune(strings.TrimSpace(it.Title))
	if len(t) > 24 {
		t = append(t[:24], '…')
	}
	if it.Nickname != "" {
		return it.Nickname + "：" + string(t)
	}
	return string(t)
}
