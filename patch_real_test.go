package main

import (
	"os"
	"testing"
)

func TestPatchRealFile(t *testing.T) {
	src := os.Getenv("REAL_JS")
	if src == "" {
		t.Skip()
	}
	b, _ := os.ReadFile(src)
	out, hits := patchFinderJS("virtual_svg-icons-register.publishX.js", string(b))
	t.Log(hits)
	os.WriteFile(os.Getenv("OUT_JS"), []byte(out), 0644)
}
