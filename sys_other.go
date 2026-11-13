//go:build !windows && !darwin

package main

import "os/exec"

// 非 Windows 平台只用于开发测试：不改系统代理、不装证书。

type proxyState struct {
	Enable   uint32
	Server   string
	Override string
	AutoURL  string
}

func enableSystemProxy(addr string) (string, error) { return "", nil }
func restoreSystemProxy()                           {}
func certInstalled() bool                           { return true }
func installCert(path string) error                 { return nil }
func openURL(u string)                              {}
func openFolder(p string)                           {}
func setupConsole()                                 {}

const certHint = ""

func alert(msg string) {}

var savedProxy *proxyState

func proxyIsOurs() bool                  { return false }
func waitForExit(pid int)                {}
func hideWindow(cmd *exec.Cmd)           {}
func revealFile(p string)                {}
func chooseFolder(current string) string { return "" }

func readClipboard() string { return "" }

func processAlive(pid int) bool           { return false }
func readProxy() proxyState               { return proxyState{} }
func upstreamFromState(proxyState) string { return "" }
