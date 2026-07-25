package main

import (
	"strings"
	"testing"

	"github.com/ShinteLab/kicho/httpapi"
)

func TestAccessURLsLoopback(t *testing.T) {
	got := accessURLs("127.0.0.1", 3000)
	want := []string{"http://127.0.0.1:3000"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("accessURLs = %v, want %v", got, want)
	}
}

func TestAccessURLsRejectsUnusablePort(t *testing.T) {
	if got := accessURLs("127.0.0.1", 0); got != nil {
		t.Errorf("accessURLs(port 0) = %v, want nil", got)
	}
}

// LAN 公開時は localhost 版を先頭に、実際に到達できる LAN アドレスも返す。
func TestAccessURLsPublicIncludesLoopbackFirst(t *testing.T) {
	got := accessURLs("0.0.0.0", 3000)
	if len(got) == 0 {
		t.Fatal("accessURLs returned nothing")
	}
	if got[0] != "http://127.0.0.1:3000" {
		t.Errorf("first URL = %q, want the loopback one", got[0])
	}
	for _, u := range got {
		if !strings.HasPrefix(u, "http://") || !strings.HasSuffix(u, ":3000") {
			t.Errorf("unexpected URL %q", u)
		}
		// 0.0.0.0 そのものは貼っても届かないので出さない。
		if strings.Contains(u, "0.0.0.0") {
			t.Errorf("URL must not contain the bind wildcard: %q", u)
		}
	}
}

// UI の「URL コピー」が作る文字列。httpapi のパスと連結できていること。
func TestComposeURLs(t *testing.T) {
	bases := []string{"http://127.0.0.1:3000", "http://192.168.1.5:3000"}

	got := composeURLs(bases, httpapi.KifuPath("abc-123"))
	want := []string{
		"http://127.0.0.1:3000/kifu/abc-123",
		"http://192.168.1.5:3000/kifu/abc-123",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("composeURLs[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	got = composeURLs(bases[:1], httpapi.RyuohKifuPath("67037f045dcc1abf6b9528e3"))
	if got[0] != "http://127.0.0.1:3000/ryuoh/kifu/67037f045dcc1abf6b9528e3" {
		t.Errorf("composeURLs = %q", got[0])
	}
}

func TestComposeURLsEmpty(t *testing.T) {
	if got := composeURLs(nil, "/kifu/x"); len(got) != 0 {
		t.Errorf("composeURLs(nil) = %v, want empty", got)
	}
}
