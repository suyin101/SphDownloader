package main

import (
	"encoding/json"
	"os"
	"time"
)

// 下载记录：已经下载过的视频（按视频 ID）不会再自动下载，重启软件后也记得。
type histEntry struct {
	File     string    `json:"file"`
	LinkKey  string    `json:"linkKey,omitempty"`
	Title    string    `json:"title"`
	Nickname string    `json:"nickname"`
	Cover    string    `json:"cover"`
	Time     time.Time `json:"time"`
}

func (s *Store) loadHistory(path string) {
	s.histPath = path
	s.history = map[string]histEntry{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.history)
	}
}

func (s *Store) saveHistoryLocked() {
	if s.histPath == "" {
		return
	}
	b, _ := json.MarshalIndent(s.history, "", " ")
	tmp := s.histPath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, s.histPath)
	}
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// downloadedLocked 这个视频以前下载过、文件也还在，返回文件路径。
func (s *Store) downloadedLocked(id string) string {
	if h, ok := s.history[id]; ok && fileExists(h.File) {
		return h.File
	}
	return ""
}

// downloadedLinkLocked 这个链接以前下载过，返回视频 ID。
func (s *Store) downloadedLinkLocked(linkKey string) (string, histEntry) {
	for id, h := range s.history {
		if h.LinkKey == linkKey && fileExists(h.File) {
			return id, h
		}
	}
	return "", histEntry{}
}

func (s *Store) recordLocked(it *Item) {
	if s.history == nil {
		s.history = map[string]histEntry{}
	}
	s.history[it.ID] = histEntry{File: it.File, LinkKey: it.LinkKey, Title: it.Title, Nickname: it.Nickname, Cover: it.Cover, Time: time.Now()}
	s.saveHistoryLocked()
}
