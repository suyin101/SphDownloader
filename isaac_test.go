package main

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

func TestKeystreamMatchesReference(t *testing.T) {
	for _, seed := range []uint64{1, 123456789, 2398474712, 18446744073709551615, rand.Uint64()} {
		data := make([]byte, 200000)
		rand.Read(data)
		want := append([]byte(nil), data...)
		refDecryptData(want, encryptedLen, seed)
		got, _ := io.ReadAll(newDecryptReader(bytes.NewReader(data), seed))
		if !bytes.Equal(got, want) {
			t.Fatalf("seed %d mismatch", seed)
		}
		if bytes.Equal(got[encryptedLen:], data[encryptedLen:]) == false {
			t.Fatal("tail should be untouched")
		}
	}
}

func TestDecryptSmallReads(t *testing.T) {
	data := make([]byte, 140000)
	rand.Read(data)
	want := append([]byte(nil), data...)
	refDecryptData(want, encryptedLen, 42)
	r := newDecryptReader(bytes.NewReader(data), 42)
	var out bytes.Buffer
	buf := make([]byte, 7)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatal("small reads mismatch")
	}
}
