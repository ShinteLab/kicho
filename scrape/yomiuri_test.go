package scrape

import "testing"

const fixturePage = `<!DOCTYPE html><html><body>
  <div class="p-article">
    <iframe class="p-other-iframe" src="https://example.com/ad/"></iframe>
    <iframe class="p-shougi-iframe js-lazy" src="https://www.yomiuri.co.jp/kifu/s/67037f045dcc1abf6b9528e3/"></iframe>
  </div>
</body></html>`

func TestFindKifuIframeSrc(t *testing.T) {
	got, err := findKifuIframeSrc(fixturePage)
	if err != nil {
		t.Fatalf("findKifuIframeSrc error: %v", err)
	}
	want := "https://www.yomiuri.co.jp/kifu/s/67037f045dcc1abf6b9528e3/"
	if got != want {
		t.Errorf("= %q, want %q", got, want)
	}
}

func TestFindKifuIframeSrcNotFound(t *testing.T) {
	if _, err := findKifuIframeSrc(`<html><body><p>no iframe</p></body></html>`); err == nil {
		t.Error("expected error when the iframe is absent")
	}
}

func TestKifuIDFromViewerURL(t *testing.T) {
	cases := map[string]string{
		"https://www.yomiuri.co.jp/kifu/s/67037f045dcc1abf6b9528e3/": "67037f045dcc1abf6b9528e3",
		"https://www.yomiuri.co.jp/kifu/s/67037f045dcc1abf6b9528e3":  "67037f045dcc1abf6b9528e3",
		"/kifu/s/abc123/": "abc123",
	}
	for raw, want := range cases {
		got, err := KifuIDFromViewerURL(raw)
		if err != nil {
			t.Errorf("KifuIDFromViewerURL(%q) error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("KifuIDFromViewerURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestKifuIDFromViewerURLRejectsUnexpectedShape(t *testing.T) {
	for _, raw := range []string{
		"https://www.yomiuri.co.jp/igoshougi/ryuoh/",
		"https://www.yomiuri.co.jp/kifu/s/",
		"https://www.yomiuri.co.jp/",
	} {
		if _, err := KifuIDFromViewerURL(raw); err == nil {
			t.Errorf("KifuIDFromViewerURL(%q) succeeded, want error", raw)
		}
	}
}

func TestPayloadURLEscapes(t *testing.T) {
	got := PayloadURL("abc/../evil")
	want := "https://www.yomiuri.co.jp/kifu/s/abc%2F..%2Fevil/_payload.js"
	if got != want {
		t.Errorf("PayloadURL() = %q, want %q", got, want)
	}
}

func TestHasClass(t *testing.T) {
	if !hasClass("p-shougi-iframe js-lazy", "p-shougi-iframe") {
		t.Error("should match a class among several")
	}
	// 部分一致で誤爆しないこと
	if hasClass("p-shougi-iframe-inner", "p-shougi-iframe") {
		t.Error("must not match a partial class name")
	}
}
