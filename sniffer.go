package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
)

// Sniffer 嗅探开关：开 = 监听代理端口 + 设系统代理；关 = 还原系统代理 + 关端口 + 断开所有连接。
type Sniffer struct {
	mu    sync.Mutex
	ca    tls.Certificate
	store *Store
	ln    net.Listener
	conns map[net.Conn]struct{}
	err   string
	up    string
}

func (s *Sniffer) Upstream() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.up
}

// 测试时替换掉，避免真的改系统代理
var (
	setSysProxy   = enableSystemProxy
	unsetSysProxy = restoreSystemProxy
)

type trackedListener struct {
	net.Listener
	s *Sniffer
}

func (l *trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.s.mu.Lock()
	l.s.conns[c] = struct{}{}
	l.s.mu.Unlock()
	return &trackedConn{Conn: c, s: l.s}, nil
}

type trackedConn struct {
	net.Conn
	s    *Sniffer
	once sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.s.mu.Lock()
		delete(c.s.conns, c.Conn)
		c.s.mu.Unlock()
	})
	return c.Conn.Close()
}

func (s *Sniffer) On() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln != nil
}

func (s *Sniffer) LastErr() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Sniffer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return nil
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort))
	if err != nil {
		s.err = fmt.Sprintf("端口 %d 被别的程序占用了", proxyPort)
		return fmt.Errorf("%s", s.err)
	}
	upstream, err := setSysProxy(fmt.Sprintf("127.0.0.1:%d", proxyPort))
	if err != nil {
		ln.Close()
		s.err = "设置系统代理失败：" + err.Error()
		return fmt.Errorf("%s", s.err)
	}
	if upstream != "" {
		logf("检测到你原来开着代理 %s，其它网站会继续走它", upstream)
	}
	s.conns = map[net.Conn]struct{}{}
	s.up = upstream
	s.ln = &trackedListener{Listener: ln, s: s}
	s.err = ""
	go http.Serve(s.ln, newProxy(s.ca, s.store, upstream))
	logf("嗅探已开启（端口 %d）", proxyPort)
	return nil
}

func (s *Sniffer) Stop() {
	s.mu.Lock()
	ln := s.ln
	s.ln = nil
	var conns []net.Conn
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.conns = map[net.Conn]struct{}{}
	s.mu.Unlock()
	unsetSysProxy()
	if ln == nil {
		return
	}
	ln.Close()
	for _, c := range conns {
		c.Close()
	}
	logf("嗅探已关闭：网络设置已还原，端口 %d 已释放", proxyPort)
}
