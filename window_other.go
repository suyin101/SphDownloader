//go:build !windows && !darwin

package main

func runWindow(url string) { openURL(url); select {} }
