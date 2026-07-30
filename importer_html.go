package kicho

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// 棋譜中継ページ(HTML)から .kif の在り処を見つけるための解析。
//
// **これはスクレイピングではない。** ページから対局内容を読み取るのではなく、
// 「この HTML はどの .kif を表示しているか」を1つ取り出すだけで、
// 中身は URL 取り込みの通常経路（原本をそのまま保存）に流す。
//
// 想定している入り口は日本将棋連盟の棋譜中継
// (`http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html` など)。
// 中継ページは JS の定数 `KIF_FILE_NAME` に .kif のパスを持っており、
// 同じ名前で拡張子を替えただけの `.kif` が隣に置かれている。

// kifFileNamePattern は中継ページの `const KIF_FILE_NAME = "/oui/.../xxx.kif";` を拾う。
// 連盟の中継ビューア(kj.js)がこの定数を見て棋譜を読み込んでいる。
var kifFileNamePattern = regexp.MustCompile(`KIF_FILE_NAME\s*=\s*["']([^"']+)["']`)

// looksLikeHTML は取得した本文が HTML かどうかを判定する。
//
// Content-Type ではなく中身で見る。KIF は必ず数字・漢字・`#` などで始まり
// `<` で始まることはないので、これで取り違えない。
func looksLikeHTML(b []byte) bool {
	// BOM(\ufeff)付きの .kif があるので空白と一緒に落とす。
	return strings.HasPrefix(strings.TrimLeft(string(b), " \t\r\n\ufeff"), "<")
}

// kifURLFromHTML は棋譜中継ページの HTML から .kif の URL を取り出す。
// base はその HTML を取得した URL(相対パスの解決に使う)。
func kifURLFromHTML(base *url.URL, page []byte) (*url.URL, error) {
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
