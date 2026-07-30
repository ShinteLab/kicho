package scrape

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// relayPage は日本将棋連盟の棋譜中継ページ
// (http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html) を写したもの。
// 中継ビューア(kj.js)が読む KIF_FILE_NAME に .kif の在り処が入っている。
const relayPage = `<!doctype html>
<html>
<head>
<meta charset="UTF-8">
<title>2026年7月29日～7月30日　七番勝負　第３局</title>
</head>
<body id="oui">
<main>
  <script language="javascript" type="text/javascript">
    const KJ_DIR = "/common/js/kj/";
    const KIF_FILE_NAME = "/oui/kifu/67/oui202607290101.kif";
    const UPDATE_TIME = 1;
  </script>
  <script src="/common/js/kj/kj.js"></script>
</main>
</body>
</html>
`

func toShiftJIS(t *testing.T, s string) []byte {
	t.Helper()
	b, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatalf("encode Shift_JIS: %v", err)
	}
	return b
}

// 世に出回る .kif は Shift_JIS が多い(連盟の中継もそう)。判別できること。
func TestDecodeKIF(t *testing.T) {
	const sample = "棋戦：テスト棋戦\n1 ７六歩(77)\n"

	got, enc, err := DecodeKIF([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if got != sample || enc != EncodingUTF8 {
		t.Errorf("UTF-8: = %q, %q", got, enc)
	}

	got, enc, err = DecodeKIF(toShiftJIS(t, sample))
	if err != nil {
		t.Fatal(err)
	}
	if got != sample || enc != EncodingShiftJIS {
		t.Errorf("Shift_JIS: = %q, %q", got, enc)
	}
}

// 中継ページの .kif が取得元 URL を基準に解決されること。
func TestKifURLFromHTML(t *testing.T) {
	base, err := url.Parse("http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html")
	if err != nil {
		t.Fatal(err)
	}
	const want = "http://live.shogi.or.jp/oui/kifu/67/oui202607290101.kif"

	t.Run("KIF_FILE_NAME", func(t *testing.T) {
		got, err := KifURLFromHTML(base, []byte(relayPage))
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want {
			t.Errorf("= %q, want %q", got, want)
		}
	})

	t.Run("相対パス", func(t *testing.T) {
		page := []byte(`<html><script>const KIF_FILE_NAME = "oui202607290101.kif";</script></html>`)
		got, err := KifURLFromHTML(base, page)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want {
			t.Errorf("= %q, want %q", got, want)
		}
	})

	// KIF_FILE_NAME を持たないサイト向けの保険。
	t.Run("aリンク", func(t *testing.T) {
		page := []byte(`<html><body><a href="/x/y.kif?v=2">棋譜</a></body></html>`)
		got, err := KifURLFromHTML(base, page)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != "http://live.shogi.or.jp/x/y.kif?v=2" {
			t.Errorf("= %q", got)
		}
	})

	t.Run("見つからない", func(t *testing.T) {
		_, err := KifURLFromHTML(base, []byte(`<html><body>棋譜はありません</body></html>`))
		if err == nil {
			t.Fatal("エラーにならなかった")
		}
		if !strings.Contains(err.Error(), ".kif") {
			t.Errorf("何を指定すべきか分からないエラー: %v", err)
		}
	})
}

func TestLooksLikeHTML(t *testing.T) {
	if !LooksLikeHTML([]byte("\n  <!doctype html>")) {
		t.Error("HTML を HTML と判定できていない")
	}
	// KIF は `<` で始まらない(BOM 付きでも)。
	kifs := []string{
		"開始日時：2026/07/29 09:00\n",
		"# --- Kifu for Windows Pro V7.20 棋譜ファイル ---\n",
		"\ufeff開始日時：2026/07/29\n",
	}
	for _, s := range kifs {
		if LooksLikeHTML([]byte(s)) {
			t.Errorf("KIF を HTML と誤判定: %.20q", s)
		}
	}
}
