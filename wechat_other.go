//go:build !windows && !darwin

package main

func wechatRunning() bool   { return false }
func wechatExePath() string { return "" }
func openWeChat(string)     {}
