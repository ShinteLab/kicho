package kicho

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/ShinteLab/core/kifu"

	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// Fetcher は棋譜を取ってくる側だけを切り出したもの（**DB を持たない**）。
//
// ⚠️ **DB と切り離してあるのが要点**（2026-09-12）。取得は store を 1 度も
// 触らないのに、以前は `Library` のメソッドだったので `kicho.Open`（＝DB を開く）
// を通らないと呼べなかった。利用側（ikkyoku）は「棚が開けていなくても URL の
// 棋譜は解析できる」という約束を持っているので、**DB が開けないことと
// 取得できないことを繋げてはいけない。**
//
// `Library` はこれを埋め込んでいるので、`Library.Fetch` などは今までどおり使える。
type Fetcher struct {
	yomiuri   *scrape.Yomiuri
	shogilive *scrape.ShogiLive
	shogidb2  *scrape.ShogiDB2
}

// NewFetcher は取得だけを行う Fetcher を作る（DB は要らない）。
func NewFetcher() *Fetcher {
	return &Fetcher{
		yomiuri:   scrape.NewYomiuri(),
		shogilive: scrape.NewShogiLive(),
		shogidb2:  scrape.NewShogiDB2(),
	}
}

// Fetch は棋譜を取得する（**保存はしない**）。
//
// 取得元は入力から判別する。**判別はここだけが知っていること**で、
// 呼び出し側に「どのサイトならどちら」を書かせない。
//
//   - live.shogi.or.jp の URL  → 日本将棋連盟の棋譜中継
//   - shogidb2.com の URL      → 将棋DB2
//   - yomiuri.co.jp の URL     → 読売（竜王戦）
//   - それ以外の http(s) URL   → **その中身を .kif として読む**（HTML なら辿る）
//   - URL でない文字列         → 読売の棋譜 ID
//
// ⚠️ **「それ以外の URL」を読売として解決しないこと**（2026-09-12 に直した）。
// 以前は連盟以外を全部読売に回していたので、**他サイトの .kif の URL が
// 「読売の棋譜 ID」扱いになり、意味の分からないエラーで落ちていた。**
// 利用側はそのぶん「.kif は別の口」という 2 つ目の入力欄を持つことになっていた。
//
// 対局中の棋譜も取得できる（その場合 Finished は false）。
// **手数 0（対局前）はエラーではない** —— 中継は対局開始前から棋譜を置いている。
func (f *Fetcher) Fetch(ctx context.Context, input string) (Fetched, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return Fetched{}, ErrNoInput
	}

	if scrape.IsShogiLiveURL(in) {
		id, err := f.shogilive.ResolveID(ctx, in)
		if err != nil {
			return Fetched{}, err
		}
		return f.fetchShogiLive(ctx, id)
	}
	// ⚠️ **汎用の URL より先に振り分けること。** 将棋DB2 のページは HTML で、
	// .kif へのリンクも持たないので、汎用の口に落ちると「KIF ではない」で終わる。
	if scrape.IsShogiDB2URL(in) {
		id, err := scrape.ShogiDB2GameID(in)
		if err != nil {
			return Fetched{}, err
		}
		return f.fetchShogiDB2(ctx, id)
	}
	// 中継として知っているサイト以外の URL は、中身をそのまま棋譜として読む。
	if isHTTPURL(in) && !scrape.IsYomiuriURL(in) {
		return f.fetchURL(ctx, in)
	}

	id, err := f.resolveRyuohInput(ctx, in)
	if err != nil {
		return Fetched{}, err
	}
	return f.fetchRyuoh(ctx, id)
}

// Refresh は取得済みの棋譜を取り直す（**保存はしない**）。
//
// 入力欄からの Fetch と違って**取得元が分かっている**ので、判別も
// 中継ページ → 棋譜 ID の往復も挟まらず、棋譜 ID で直接取りに行く。
//
// 保存は画面の内容をそのまま書き込む（Save はサイトへ取りに行かない）ので、
// **対局中に取ったものを後で保存するなら先にここを通す。**
// そうしないと取得した時点の古い棋譜が入る。
//
// ⚠️ **url も取り直せる**（2026-09-12）。あちらの sourceID は**取得に使った
// URL そのもの**（`sourceIDForURL`）なので、もう一度そこへ行けばよい。
// **paste だけが取り直せない**（取得元が無い）。
func (f *Fetcher) Refresh(ctx context.Context, source, sourceID string) (Fetched, error) {
	if strings.TrimSpace(sourceID) == "" {
		return Fetched{}, fmt.Errorf("%w: 取得元の棋譜 ID が空です", ErrNoInput)
	}
	switch source {
	case store.SourceShogiLive:
		return f.fetchShogiLive(ctx, sourceID)
	case store.SourceShogiDB2:
		return f.fetchShogiDB2(ctx, sourceID)
	case store.SourceYomiuri, "":
		return f.fetchRyuoh(ctx, sourceID)
	case store.SourceURL:
		return f.fetchURL(ctx, sourceID)
	default:
		// paste は取得元が無いので取り直せない。
		return Fetched{}, fmt.Errorf("%w: %s", ErrUnsupportedSource, source)
	}
}

// PreviewKIF は KIF テキストを解析して内容を返す（**保存はしない**）。
// 取り込み前に画面へ出すためのもので、ImportKIF と同じ判定を通る。
func (f *Fetcher) PreviewKIF(text string) (Fetched, error) {
	doc, err := parseImportable(text)
	if err != nil {
		return Fetched{}, err
	}
	return fromDocument(doc, store.SourcePaste, "", text, EncodingUTF8), nil
}

// PreviewURL は URL から KIF を取得して解析する（**保存はしない**）。
//
// ⚠️ **中身は `fetchURL` と同じ**（2026-09-12 に寄せた）。別々に組み立てていた
// ころは、こちらだけ `SourceID` が空で**そのまま保存できない Fetched** を
// 返していた。
func (f *Fetcher) PreviewURL(ctx context.Context, rawURL string) (Fetched, error) {
	return f.fetchURL(ctx, rawURL)
}

// fetchURL は URL の中身を .kif として読む（保存はしない）。
//
// ⚠️ **`SourceID` に URL を入れるのが要点**（2026-09-12）。以前は url 由来の
// 棋譜だけ `SourceID` が空で、保存のときに UUID を振っていた。その結果
// **同じ URL を登録し直すたびに別の棋譜として増え、取り直しもできなかった**
// （`(source, source_id)` が upsert の鍵なので、鍵が毎回変われば必ず増える）。
// URL には URL 自体という自然な鍵があるので、それを使う。
func (f *Fetcher) fetchURL(ctx context.Context, rawURL string) (Fetched, error) {
	text, encoding, err := f.FetchKIFFromURL(ctx, rawURL)
	if err != nil {
		return Fetched{}, err
	}
	doc, err := parseImportable(text)
	if err != nil {
		return Fetched{}, err
	}
	got := fromDocument(doc, store.SourceURL, rawURL, text, encoding)
	got.SourceID = sourceIDForURL(rawURL)
	return got, nil
}

// sourceIDForURL は URL を「取得元での ID」に正規化する。
//
// **同じ棋譜を指す URL が同じ ID になれば、登録し直しても増えない。**
// 揃えるのはスキームとホストの大文字小文字・既定ポート・フラグメントだけで、
// **パスとクエリは触らない**（サイトによって意味が違うので、勝手に落とすと
// 別の棋譜が同じ ID になりうる）。
//
// ⚠️ **ハッシュにしないこと。** そのままの URL なら `Refresh` がこの値へ
// 取りに行けるし、DB を覗いたときに何の棋譜か読める。
func sourceIDForURL(raw string) string {
	in := strings.TrimSpace(raw)
	u, err := url.Parse(in)
	if err != nil {
		return in
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// isHTTPURL は http(s) の URL らしいかを返す（棋譜 ID との見分け）。
func isHTTPURL(in string) bool {
	return strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://")
}

// fetchRyuoh は読売のペイロードから KIF を組み立てる（保存はしない）。
func (f *Fetcher) fetchRyuoh(ctx context.Context, id string) (Fetched, error) {
	g, err := f.yomiuri.FetchGame(ctx, id)
	if err != nil {
		return Fetched{}, err
	}
	return fromYomiuri(g), nil
}

// fetchShogiLive は連盟の中継から .kif を取る（保存はしない）。
func (f *Fetcher) fetchShogiLive(ctx context.Context, id string) (Fetched, error) {
	g, err := f.shogilive.FetchGame(ctx, id)
	if err != nil {
		return Fetched{}, err
	}
	return f.fromShogiLive(g), nil
}

// fetchShogiDB2 は将棋DB2 の構造化データから KIF を組み立てる（保存はしない）。
func (f *Fetcher) fetchShogiDB2(ctx context.Context, id string) (Fetched, error) {
	g, err := f.shogidb2.FetchGame(ctx, id)
	if err != nil {
		return Fetched{}, err
	}
	return f.fromShogiDB2(g), nil
}

// resolveRyuohInput は入力（対局ページ URL / 棋譜ビューア URL / 棋譜 ID）を
// 読売の棋譜 ID に解決する。
func (f *Fetcher) resolveRyuohInput(ctx context.Context, in string) (string, error) {
	if !isHTTPURL(in) {
		return in, nil // 棋譜 ID とみなす
	}
	// 棋譜ビューアの URL が直接貼られた場合はそこから ID を取る。
	if id, err := scrape.KifuIDFromViewerURL(in); err == nil {
		return id, nil
	}
	// 対局ページ URL → iframe を辿って棋譜 ID を得る。
	return f.yomiuri.FetchKifuID(ctx, in)
}

// parseImportable は取り込む前の KIF を解析する。
//
// **「指し手が 0 手」はエラーにしない**（対局前の中継棋譜は正当に存在する）。
// KIF かどうかはヘッダも指し手も取れなかったかどうかで判断する。
func parseImportable(text string) (kifu.Document, error) {
	if strings.TrimSpace(text) == "" {
		return kifu.Document{}, ErrEmptyKifu
	}
	doc, err := kifu.Parse(text)
	if err != nil {
		return kifu.Document{}, err
	}
	if doc.Empty() {
		return kifu.Document{}, ErrNotKifu
	}
	return doc, nil
}
