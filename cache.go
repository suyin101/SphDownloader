package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// 清除缓存：只清软件自己在数据目录里生成的东西，下载的视频、文案、设置、证书、下载记录一律不动。
//   ① 日志和临时文件：debug/（日志、调试抓的网页文件）、asr/work/（转文字中间文件）、没装完的组件残片
//   ② 转文字组件：asr/ 整个（精准模式约 1GB）；完整安装包自带的快速模式下次转文字时自动重装，其它要重新下载

func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func asrDir() string { return filepath.Join(dataDir, "asr") }

// asrTemp 组件目录里的中间文件：work/ 和下载到一半的 .part。
func asrTemp() []string {
	out := []string{filepath.Join(asrDir(), "work")}
	m, _ := filepath.Glob(filepath.Join(asrDir(), "*.part"))
	return append(out, m...)
}

func cacheSizes(store *Store) map[string]int64 {
	logs := dirSize(filepath.Join(dataDir, "debug"))
	for _, p := range asrTemp() {
		logs += dirSize(p)
	}
	asr := dirSize(asrDir())
	for _, p := range asrTemp() {
		asr -= dirSize(p)
	}
	if asr < 0 {
		asr = 0
	}
	return map[string]int64{"logs": logs, "asr": asr}
}

func clearCache(store *Store, logs, asr bool) (any, error) {
	if !logs && !asr {
		return nil, errors.New("先勾选要清除的内容")
	}
	if asr && store.tx != nil && store.tx.Busy() {
		return nil, errors.New("正在转文字或安装转文字组件，等它完成后再清")
	}
	before := cacheSizes(store)
	var freed int64
	if logs {
		traceMu.Lock()
		os.RemoveAll(filepath.Join(dataDir, "debug"))
		traceMu.Unlock()
		if store.tx == nil || !store.tx.Busy() { // 正在转文字时中间文件还要用
			for _, p := range asrTemp() {
				os.RemoveAll(p)
			}
		}
		freed += before["logs"]
	}
	if asr && store.tx != nil {
		if err := store.tx.Remove(); err != nil {
			return nil, err
		}
		freed += before["asr"]
	}
	logf("已清除缓存，腾出 %.1f MB", float64(freed)/1e6)
	return map[string]int64{"freed": freed}, nil
}
