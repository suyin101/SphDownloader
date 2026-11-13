package main

import "testing"

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"1.3.1", "1.3.0", true}, {"v1.10.0", "1.9.9", true}, {"1.3.0", "1.3.0", false}, {"1.2.9", "1.3.0", false}, {"2.0", "1.9.9", true}}
	for _, c := range cases {
		if newerVersion(c.a, c.b) != c.want {
			t.Fatalf("%s vs %s", c.a, c.b)
		}
	}
}
