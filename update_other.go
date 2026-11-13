//go:build !windows && !darwin

package main

import "fmt"

func updateDownloadPath() (string, error) { return "", fmt.Errorf("这个系统不支持自动更新") }
func installUpdate(string) error          { return fmt.Errorf("这个系统不支持自动更新") }
func cleanupOldUpdate()                   {}
