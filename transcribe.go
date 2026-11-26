package main

// transcribe.go 视频转文字（本地，不联网识别）。
// 识别用 sherpa-onnx 的 vad-with-offline-asr 命令行 + SenseVoice 小模型（int8，自带标点），
// 声音由 mp4audio.go 纯 Go 解出来写成 wav。组件第一次用时下载到 dataDir/asr（约 250MB），软件本体不变大。
// 转好的文案存成和视频同名的 .txt（只有文字，带标点、按停顿分段）。

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 组件放在 GitHub 的 asr-v1 发布里（不是 latest，不影响自动更新）；模型优先走国内镜像。
var asrReleaseBase = "https://github.com/suyin101/SphDownloader/releases/download/asr-v1/"

type asrPart struct {
	Name string   // 存到 asr 目录下的名字
	Size int64    // 用来算进度、判断下载完整
	SHA  string   // sha256
	URLs []string // 依次尝试
	Zip  bool     // 下载后解压
}

func asrRuntimeName() string {
	switch runtime.GOOS + "-" + runtime.GOARCH {
	case "darwin-arm64":
		return "asr-runtime-mac-arm64.zip"
	case "darwin-amd64":
		return "asr-runtime-mac-x86_64.zip"
	case "windows-amd64":
		return "asr-runtime-windows-amd64.zip"
	}
	return ""
}

var asrRuntimeInfo = map[string][2]string{ // 大小, sha256
	"asr-runtime-mac-arm64.zip":     {"8972079", "9739545dc29b1f9e35b1f7dc60008b979f215bcf026378806aa3adcd5f63bd23"},
	"asr-runtime-mac-x86_64.zip":    {"10389079", "3e5c5533d02669a8888432d44d061d4890af356a8fb523a021f16d9dea9251de"},
	"asr-runtime-windows-amd64.zip": {"7844681", "5428cdc790601a7fa9849f7cc65c0c3dbfa6cf2c7bd3fa492ee7609e3511e5de"},
}

const hfModel = "csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/resolve/main/"

var asrParts = defaultASRParts

func defaultASRParts() []asrPart {
	rt := asrRuntimeName()
	info := asrRuntimeInfo[rt]
	size, _ := strconv.ParseInt(info[0], 10, 64)
	return []asrPart{
		{Name: rt, Size: size, SHA: info[1], URLs: []string{asrReleaseBase + rt}, Zip: true},
		{Name: "model.int8.onnx", Size: 239233841, SHA: "c71f0ce00bec95b07744e116345e33d8cbbe08cef896382cf907bf4b51a2cd51",
			URLs: []string{"https://hf-mirror.com/" + hfModel + "model.int8.onnx", "https://huggingface.co/" + hfModel + "model.int8.onnx", asrReleaseBase + "model.int8.onnx"}},
		{Name: "tokens.txt", Size: 315894, SHA: "f449eb28dc567533d7fa59be34e2abca8784f771850c78a47fb731a31429a1dc",
			URLs: []string{"https://hf-mirror.com/" + hfModel + "tokens.txt", "https://huggingface.co/" + hfModel + "tokens.txt", asrReleaseBase + "tokens.txt"}},
	}
}

// ASRState 转文字组件的状态（界面显示用）。
type ASRState struct {
	Status   string `json:"status"` // missing 未安装 / downloading 下载中 / ready 可用 / error 下载失败
	Progress int64  `json:"progress"`
	Total    int64  `json:"total"`
	Err      string `json:"err,omitempty"`
}

type Transcriber struct {
	dir   string
	mu    sync.Mutex
	st    ASRState
	queue chan *Item
	store *Store
	dlMu  sync.Mutex // 同一时间只下载一次组件
}

func NewTranscriber(dir string, store *Store) *Transcriber {
	t := &Transcriber{dir: dir, store: store, queue: make(chan *Item, 2000)}
	t.st.Status = "missing"
	if t.installed() {
		t.st.Status = "ready"
	}
	t.st.Total = t.totalSize()
	go t.worker() // 一次只转一个，不和下载抢电脑
	return t
}

func (t *Transcriber) State() ASRState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

func (t *Transcriber) set(f func(s *ASRState)) {
	t.mu.Lock()
	f(&t.st)
	t.mu.Unlock()
}

func (t *Transcriber) totalSize() int64 {
	var n int64
	for _, p := range asrParts() {
		n += p.Size
	}
	return n
}

func (t *Transcriber) exe() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(t.dir, "sherpa-asr.exe")
	}
	return filepath.Join(t.dir, "sherpa-asr")
}

// installed 每个文件都在、大小对得上（解压出来的程序看标记文件）。
func (t *Transcriber) installed() bool {
	if asrRuntimeName() == "" {
		return false
	}
	for _, p := range asrParts() {
		if p.Zip {
			b, err := os.ReadFile(filepath.Join(t.dir, p.Name+".ok"))
			if err != nil || strings.TrimSpace(string(b)) != p.SHA || !fileExists(t.exe()) {
				return false
			}
			continue
		}
		st, err := os.Stat(filepath.Join(t.dir, p.Name))
		if err != nil || st.Size() != p.Size {
			return false
		}
	}
	return true
}

// ensure 没装就下载（带进度，断了能接着下）。
func (t *Transcriber) ensure() error {
	t.dlMu.Lock()
	defer t.dlMu.Unlock()
	if t.installed() {
		t.set(func(s *ASRState) { s.Status, s.Err = "ready", "" })
		return nil
	}
	if asrRuntimeName() == "" {
		return fmt.Errorf("这台电脑的系统暂不支持转文字")
	}
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return err
	}
	logf("第一次转文字，正在下载转文字组件（约 %d MB，只需下载一次）", t.totalSize()/1e6)
	var done int64
	t.set(func(s *ASRState) { s.Status, s.Err, s.Progress, s.Total = "downloading", "", 0, t.totalSize() })
	for _, p := range asrParts() {
		if err := t.fetchPart(p, done); err != nil {
			t.set(func(s *ASRState) { s.Status, s.Err = "error", err.Error() })
			return fmt.Errorf("转文字组件下载失败：%v", err)
		}
		done += p.Size
	}
	if !t.installed() {
		err := errors.New("组件文件不完整，请重试")
		t.set(func(s *ASRState) { s.Status, s.Err = "error", err.Error() })
		return err
	}
	t.set(func(s *ASRState) { s.Status, s.Progress = "ready", s.Total })
	logf("转文字组件已装好")
	return nil
}

func (t *Transcriber) fetchPart(p asrPart, base int64) error {
	final := filepath.Join(t.dir, p.Name)
	if !p.Zip {
		if st, err := os.Stat(final); err == nil && st.Size() == p.Size {
			t.set(func(s *ASRState) { s.Progress = base + p.Size })
			return nil
		}
	} else if b, err := os.ReadFile(final + ".ok"); err == nil && strings.TrimSpace(string(b)) == p.SHA && fileExists(t.exe()) {
		t.set(func(s *ASRState) { s.Progress = base + p.Size })
		return nil
	}
	tmp := final + ".part"
	var lastErr error
	for _, u := range p.URLs {
		for try := 0; try < 3; try++ {
			err := t.download(u, tmp, p.Size, base)
			if err == nil {
				break
			}
			lastErr = err
			logf("下载 %s 出错（%v），重试", p.Name, err)
			if errors.Is(err, errBadSource) {
				break // 这个地址不行，换下一个
			}
			time.Sleep(time.Second)
		}
		st, err := os.Stat(tmp)
		if err != nil || st.Size() != p.Size {
			continue
		}
		if sum, _ := fileSHA(tmp); sum != p.SHA {
			os.Remove(tmp)
			lastErr = fmt.Errorf("%s 校验不通过", p.Name)
			continue
		}
		if p.Zip {
			if err := unzipTo(tmp, t.dir); err != nil {
				os.Remove(tmp)
				return fmt.Errorf("解压失败：%v", err)
			}
			os.Remove(tmp)
			return os.WriteFile(final+".ok", []byte(p.SHA), 0o644)
		}
		return os.Rename(tmp, final)
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载地址")
	}
	return lastErr
}

var errBadSource = errors.New("下载地址不可用")

var asrClient = func() *http.Client { return githubClient(2 * time.Hour) }

// download 下载到 tmp，已经下了一部分就接着下。
func (t *Transcriber) download(u, tmp string, size, base int64) error {
	var have int64
	if st, err := os.Stat(tmp); err == nil {
		have = st.Size()
		if have >= size {
			return nil
		}
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "SphDownloader/"+version)
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := asrClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flag := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case 206:
		flag |= os.O_APPEND
	case 200:
		flag |= os.O_TRUNC
		have = 0
	default:
		return fmt.Errorf("%w（服务器返回 %d）", errBadSource, resp.StatusCode)
	}
	if resp.StatusCode == 200 && resp.ContentLength > 0 && resp.ContentLength != size {
		return fmt.Errorf("%w（文件大小不对）", errBadSource)
	}
	f, err := os.OpenFile(tmp, flag, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	pr := &progressReader{r: io.LimitReader(resp.Body, size-have), onRead: func(n int64) {
		have += n
		t.set(func(s *ASRState) { s.Progress = base + have })
	}}
	t.set(func(s *ASRState) { s.Progress = base + have })
	if _, err := io.Copy(f, pr); err != nil {
		return err
	}
	if have != size {
		return fmt.Errorf("下载中断（%d/%d）", have, size)
	}
	return nil
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func unzipTo(src, dir string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		name := filepath.Base(zf.Name) // 包里只有平铺的几个文件
		if zf.FileInfo().IsDir() || name == "." || name == ".." {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		out := filepath.Join(dir, name)
		tmp := out + ".tmp"
		w, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		w.Close()
		if err != nil {
			os.Remove(tmp)
			return err
		}
		os.Remove(out)
		if err := os.Rename(tmp, out); err != nil {
			return err
		}
	}
	return nil
}

// ---- 排队转文字 ----

func (t *Transcriber) Enqueue(it *Item) bool {
	select {
	case t.queue <- it:
		return true
	default:
		return false
	}
}

func (t *Transcriber) worker() {
	for it := range t.queue {
		s := t.store
		s.mu.Lock()
		if it.Tx != "queued" || it.File == "" || s.items[it.Key] != it { // 排队期间被移除了
			s.mu.Unlock()
			continue
		}
		it.Tx, it.TxPct = "running", 0
		file, info := it.File, it.FeedInfo
		s.mu.Unlock()

		txt, err := t.run(file, info, func(pct int) {
			s.mu.Lock()
			if pct > it.TxPct {
				it.TxPct = pct
			}
			s.mu.Unlock()
		})
		s.mu.Lock()
		if err != nil {
			it.Tx, it.TxErr = "error", err.Error()
			logf("转文字失败：%s（%v）", label(it), err)
		} else {
			it.Tx, it.TxFile, it.TxErr = "done", txt, ""
			logf("转文字完成：%s", filepath.Base(txt))
		}
		s.mu.Unlock()
	}
}

// run 转一个视频，返回 .txt 路径。
// onPct 报进度 0~100：解出声音算 8%，后面按识别到第几秒算。
func (t *Transcriber) run(file string, info FeedInfo, onPct func(int)) (string, error) {
	if onPct == nil {
		onPct = func(int) {}
	}
	if err := t.ensure(); err != nil {
		return "", err
	}
	pcm, rate, err := decodeMP4Audio(file)
	if err != nil {
		return "", err
	}
	onPct(5)
	if len(pcm) < rate/2 {
		return "", errors.New("没识别到人声（视频没有声音）")
	}
	work := filepath.Join(t.dir, "work")
	os.MkdirAll(work, 0o755)
	wav := filepath.Join(work, "job.wav")
	defer os.Remove(wav)
	if err := writeWav(wav, pcm, rate); err != nil {
		return "", err
	}
	secs := float64(len(pcm)) / float64(rate)
	onPct(8)
	segs, err := t.recognize(secs, func(at float64) {
		if secs > 0 {
			onPct(8 + int(at/secs*91))
		}
	})
	if err != nil {
		return "", err
	}
	body := formatTranscript(segs)
	if body == "" {
		return "", errors.New("没识别到人声（可能是纯音乐或只有花字）")
	}
	txt := strings.TrimSuffix(file, filepath.Ext(file)) + ".txt"
	if err := writeTranscript(txt, info, secs, body); err != nil {
		return "", err
	}
	return txt, nil
}

type asrSeg struct {
	Start, End float64
	Text       string
}

var segLine = regexp.MustCompile(`^\s*([0-9.]+)\s+--\s+([0-9.]+):\s?(.*)$`)

// recognize 调命令行识别 work/job.wav。用相对路径，避免 Windows 用户名里有中文时程序读不到文件。
// onAt 每识别出一句就报一次“识别到第几秒”（程序是边识别边输出的）。
func (t *Transcriber) recognize(secs float64, onAt func(float64)) ([]asrSeg, error) {
	threads := runtime.NumCPU() / 2
	if threads < 1 {
		threads = 1
	}
	if threads > 4 {
		threads = 4
	}
	limit := time.Duration(secs*2)*time.Second + 2*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.exe(),
		"--silero-vad-model=silero_vad.onnx",
		"--sense-voice-model=model.int8.onnx",
		"--tokens=tokens.txt",
		"--sense-voice-use-itn=1",
		"--sense-voice-language=auto",
		"--num-threads="+strconv.Itoa(threads),
		filepath.Join("work", "job.wav"))
	cmd.Dir = t.dir
	hideWindow(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("识别程序启动失败：%v", err)
	}
	var out strings.Builder
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		out.WriteString(line + "\n")
		if m := segLine.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil && onAt != nil {
			if at, e := strconv.ParseFloat(m[2], 64); e == nil {
				onAt(at)
			}
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil, errors.New("转文字超时了")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		tracef("[转文字] 出错 %v\n%s", err, stderr.String())
		return nil, fmt.Errorf("识别程序出错：%v %s", err, msg)
	}
	return parseSegments(out.String()), nil
}

func parseSegments(out string) []asrSeg {
	var segs []asrSeg
	for _, line := range strings.Split(out, "\n") {
		m := segLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		txt := strings.TrimSpace(m[3])
		if txt == "" {
			continue
		}
		segs = append(segs, asrSeg{a, b, txt})
	}
	return segs
}

var sentenceEnd = regexp.MustCompile(`[。！？!?…]$`)

// formatTranscript 句子连起来；停顿超过 1.2 秒、或一段太长时另起一段。
func formatTranscript(segs []asrSeg) string {
	var paras []string
	var cur strings.Builder
	prevEnd := -1.0
	for _, sg := range segs {
		text := tidyText(sg.Text)
		if text == "" {
			continue
		}
		n := len([]rune(cur.String()))
		if cur.Len() > 0 && ((sg.Start-prevEnd > 1.2 && n >= 30) || n >= 160) {
			paras = append(paras, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			last := cur.String()
			if !sentenceEnd.MatchString(last) && !strings.HasSuffix(last, "，") && !strings.HasSuffix(last, "、") {
				cur.WriteString("，")
			}
		}
		cur.WriteString(text)
		prevEnd = sg.End
	}
	if cur.Len() > 0 {
		paras = append(paras, cur.String())
	}
	return strings.Join(paras, "\n\n")
}

var spaceAroundCJK = regexp.MustCompile(`([\p{Han}，。！？、：；])\s+|\s+([\p{Han}，。！？、：；])`)

func tidyText(s string) string {
	s = strings.TrimSpace(s)
	s = spaceAroundCJK.ReplaceAllString(s, "$1$2")
	return s
}

func writeTranscript(path string, f FeedInfo, secs float64, body string) error {
	var b strings.Builder
	title := strings.TrimSpace(f.Title)
	if title != "" {
		b.WriteString(title + "\n")
	}
	var meta []string
	if f.Nickname != "" {
		meta = append(meta, "作者："+f.Nickname)
	}
	if f.CreateTime > 0 {
		meta = append(meta, "发布："+time.Unix(f.CreateTime, 0).Format("2006-01-02"))
	}
	meta = append(meta, fmt.Sprintf("时长：%d:%02d", int(secs)/60, int(secs)%60))
	b.WriteString(strings.Join(meta, "　") + "\n\n")
	b.WriteString(body + "\n")
	text := b.String()
	if runtime.GOOS == "windows" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, append([]byte("\xEF\xBB\xBF"), text...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readTranscript 给界面显示：去掉开头的 BOM，统一换行。
func readTranscript(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("文案文件不见了，可能被移走或删除了")
	}
	s := strings.TrimPrefix(string(b), "\xEF\xBB\xBF")
	return strings.ReplaceAll(s, "\r\n", "\n"), nil
}

// txtFor 视频旁边同名的 .txt
func txtFor(video string) string {
	if video == "" || strings.EqualFold(filepath.Ext(video), "") {
		return ""
	}
	return strings.TrimSuffix(video, filepath.Ext(video)) + ".txt"
}
