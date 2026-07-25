// Package scrape は将棋情報サイトから棋譜を取得する。
//
// Wails には依存しない。取得した結果は core/kifu の Document として返し、
// KIF テキストの組み立ては core 側の仕様に委ねる。
package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"shinte/core/kifu"
)

// Yomiuri は読売(竜王戦)の棋譜ページを扱う。
type Yomiuri struct {
	Client *http.Client
}

// NewYomiuri は既定の HTTP クライアントを持つ Yomiuri を返す。
func NewYomiuri() *Yomiuri {
	return &Yomiuri{Client: &http.Client{Timeout: 30 * time.Second}}
}

func (y *Yomiuri) client() *http.Client {
	if y.Client != nil {
		return y.Client
	}
	return http.DefaultClient
}

// Game は取得した1局分の棋譜と対局情報。
type Game struct {
	SourceID  string    // 読売の棋譜 ID
	Event     string    // 棋戦名
	Handicap  string    // 手合割
	Place     string    // 対局場所
	Black     string    // 先手
	White     string    // 後手
	StartedAt time.Time // 開始日時
	// EndMark は終局の種別(例 "投了")。対局中は空。
	// 対局中の棋譜は随時更新されるため、保存済みかどうかの判断材料になる。
	EndMark string
	// TimeLimit は持ち時間、Countdown は秒読み。
	TimeLimit time.Duration
	Countdown time.Duration
	Moves     []kifu.Move
}

// Finished は終局しているかどうかを返す(対局中なら false)。
func (g Game) Finished() bool { return g.EndMark != "" }

// Document は Game を core/kifu の Document に変換する。
//
// 読売のペイロードは各手の消費時間(spend)を持つので ShowTime を立てる。
// ただし分単位までしか記録されていない(spend は常に 60 の倍数)。
func (g Game) Document() kifu.Document {
	return kifu.Document{
		Handicap:  g.Handicap,
		Event:     g.Event,
		Place:     g.Place,
		StartedAt: g.StartedAt,
		Black:     g.Black,
		White:     g.White,
		TimeLimit: g.TimeLimit,
		Countdown: g.Countdown,
		ShowTime:  true,
		Moves:     g.Moves,
	}
}

// KIF は Game を KIF テキストに変換する。
func (g Game) KIF() string { return g.Document().String() }

func (y *Yomiuri) get(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := y.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ViewerURL は棋譜 ID から棋譜ビューアの URL を組み立てる。
// 保存した棋譜の諸元(どこから取ったか)として残す。
func ViewerURL(id string) string {
	return "https://www.yomiuri.co.jp/kifu/s/" + url.PathEscape(id) + "/"
}

// PayloadURL は棋譜 ID からペイロード URL を組み立てる。
func PayloadURL(id string) string {
	return ViewerURL(id) + "_payload.js"
}

// FetchGame は棋譜 ID から1局分を取得する。
func (y *Yomiuri) FetchGame(ctx context.Context, id string) (*Game, error) {
	if id == "" {
		return nil, fmt.Errorf("scrape: empty kifu id")
	}
	src, err := y.get(ctx, PayloadURL(id))
	if err != nil {
		return nil, err
	}
	g, err := parsePayload(src)
	if err != nil {
		return nil, fmt.Errorf("scrape %s: %w", id, err)
	}
	g.SourceID = id
	return g, nil
}

// FetchKifuID は対局ページ URL から棋譜 ID を取得する。
//
// 対局ページには棋譜ビューアの iframe(`iframe.p-shougi-iframe`)が埋まっており、
// その src が `https://www.yomiuri.co.jp/kifu/s/{id}/` の形になっている。
func (y *Yomiuri) FetchKifuID(ctx context.Context, pageURL string) (string, error) {
	doc, err := y.get(ctx, pageURL)
	if err != nil {
		return "", err
	}
	src, err := findKifuIframeSrc(doc)
	if err != nil {
		return "", err
	}
	id, err := KifuIDFromViewerURL(src)
	if err != nil {
		return "", err
	}
	return id, nil
}

// findKifuIframeSrc は HTML から `iframe.p-shougi-iframe` の src を取り出す。
func findKifuIframeSrc(doc string) (string, error) {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "iframe" {
			var class, src string
			for _, a := range n.Attr {
				switch a.Key {
				case "class":
					class = a.Val
				case "src":
					src = a.Val
				}
			}
			if hasClass(class, "p-shougi-iframe") && src != "" {
				found = src
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if found == "" {
		return "", fmt.Errorf("'iframe.p-shougi-iframe' not found")
	}
	return found, nil
}

func hasClass(classAttr, want string) bool {
	for _, c := range strings.Fields(classAttr) {
		if c == want {
			return true
		}
	}
	return false
}

// KifuIDFromViewerURL は棋譜ビューア URL から ID 部分を取り出す。
// 期待する形は `https://www.yomiuri.co.jp/kifu/s/{id}/`。
func KifuIDFromViewerURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse viewer url %q: %w", raw, err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// 期待: ["kifu", "s", "{id}"]
	if len(parts) < 3 || parts[0] != "kifu" || parts[1] != "s" || parts[2] == "" {
		return "", fmt.Errorf("unexpected viewer url shape: %q", raw)
	}
	return parts[2], nil
}
