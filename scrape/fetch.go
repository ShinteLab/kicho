package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// 取得元によらず使う部品。文字コードの判別と、
// 棋譜中継ページ(HTML)から .kif を辿る解決。

// MaxKifuBytes は1回の取得で読む上限。棋譜1局は大きくても数十 KB なので、
// 誤って巨大なファイルを掴んだときに落とすための保険。
const MaxKifuBytes = 4 << 20 // 4MiB

// 文字コードの記録に使う名前。
const (
	EncodingUTF8     = "utf-8"
	EncodingShiftJIS = "shift_jis"
)

// DecodeKIF は棋譜のバイト列を UTF-8 文字列にし、元の文字コード名を返す。
//
// 世に出回っている .kif は **Shift_JIS が多い**(Kifu for Windows 等の既定。
// 日本将棋連盟の棋譜中継も Shift_JIS)。
// UTF-8 として妥当ならそのまま、そうでなければ Shift_JIS として解釈する。
//
// 保存する本文は UTF-8 に寄せる。元の文字コードは記録だけ残す(主にデバッグ用)。
func DecodeKIF(b []byte) (text, encoding string, err error) {
	if utf8.Valid(b) {
		return string(b), EncodingUTF8, nil
	}
	out, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), b)
	if err != nil {
		return "", "", fmt.Errorf("文字コードを判別できませんでした(UTF-8 でも Shift_JIS でもありません): %w", err)
	}
	return string(out), EncodingShiftJIS, nil
}

// ReadLimited はレスポンス本文を MaxKifuBytes まで読む。
func ReadLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxKifuBytes+1))
	if err != nil {
		return nil, fmt.Errorf("読み込みに失敗しました: %w", err)
	}
	if len(b) > MaxKifuBytes {
		return nil, fmt.Errorf("内容が大きすぎます(%d バイト超)", MaxKifuBytes)
	}
	return b, nil
}

// getBytes は URL の中身を MaxKifuBytes まで取得する。
func getBytes(ctx context.Context, c *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("取得に失敗しました: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("取得に失敗しました(HTTP %d): %s", resp.StatusCode, rawURL)
	}
	return ReadLimited(resp.Body)
}

// 棋譜中継ページ(HTML)から .kif の在り処を見つける。
//
// **これはスクレイピングではない。** ページから対局内容を読み取るのではなく、
// 「この HTML はどの .kif を表示しているか」を1つ取り出すだけで、
// 取り込むのは .kif の原本。
//
// 日本将棋連盟の棋譜中継ページは JS の定数 `KIF_FILE_NAME` に .kif のパスを持ち、
// 中継ビューア(`/common/js/kj/kj.js`)がそれを読んで棋譜を表示している。

// kifFileNamePattern は中継ページの `const KIF_FILE_NAME = "/oui/.../xxx.kif";` を拾う。
var kifFileNamePattern = regexp.MustCompile(`KIF_FILE_NAME\s*=\s*["']([^"']+)["']`)

// LooksLikeHTML は取得した本文が HTML かどうかを判定する。
//
// Content-Type ではなく中身で見る。KIF は数字・漢字・`#` などで始まり
// `<` で始まることはないので、これで取り違えない。
func LooksLikeHTML(b []byte) bool {
	// BOM(\ufeff)付きの .kif があるので空白と一緒に落とす。
	return strings.HasPrefix(strings.TrimLeft(string(b), " \t\r\n\ufeff"), "<")
}

// KifURLFromHTML は棋譜中継ページの HTML から .kif の URL を取り出す。
// base はその HTML を取得した URL(相対パスの解決に使う)。
func KifURLFromHTML(base *url.URL, page []byte) (*url.URL, error) {
	ref, err := findKifRef(string(page))
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("棋譜ファイルの URL が不正です(%q): %w", ref, err)
	}
	return base.ResolveReference(u), nil
}

// findKifRef は HTML から .kif への参照を1つ探す。
//
//  1. `KIF_FILE_NAME`(連盟の中継ページ)
//  2. `.kif` で終わる <a href>(他サイト向けの保険)
func findKifRef(page string) (string, error) {
	if m := kifFileNamePattern.FindStringSubmatch(page); m != nil {
		return m[1], nil
	}
	if href, ok := findKifAnchor(page); ok {
		return href, nil
	}
	return "", fmt.Errorf("HTML が返ってきましたが、棋譜(.kif)へのリンクが見つかりません。" +
		".kif の URL を直接指定してください")
}

// findKifAnchor は `.kif` で終わる <a href> を最初の1つだけ返す。
func findKifAnchor(page string) (string, bool) {
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return "", false
	}
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key != "href" {
					continue
				}
				// クエリやフラグメントが付いていても拾えるようにパスだけ見る。
				if u, err := url.Parse(a.Val); err == nil &&
					strings.HasSuffix(strings.ToLower(u.Path), ".kif") {
					found = a.Val
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found, found != ""
}
