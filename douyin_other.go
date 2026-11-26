//go:build !windows && !darwin

package main

import "errors"

func browserFetchDouyin(id, script string) (string, error) {
	return "", errors.New("这个系统暂不支持抖音解析")
}
