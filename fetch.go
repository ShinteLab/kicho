package kicho

import (
	"context"
	"fmt"
	"strings"

	"github.com/ShinteLab/core/kifu"

	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// Fetch はライブ中継から棋譜を取得する（**保存はしない**）。
//
// 取得元は入力から判別する。
//
//   - live.shogi.or.jp の URL  → 日本将棋連盟の棋譜中継
//   - それ以外（URL / 棋譜 ID）→ 読売（竜王戦）
//
// 対局中の棋譜も取得できる（その場合 Finished は false）。
// **手数 0（対局前）はエラーではない** —— 中継は対局開始前から棋譜を置いている。
func (l *Library) Fetch(ctx context.Context, input string) (Fetched, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return Fetched{}, ErrNoInput
	}

	if scrape.IsShogiLiveURL(in) {
		id, err := l.shogilive.ResolveID(ctx, in)
		if err != nil {
			return Fetched{}, err
		}
		return l.fetchShogiLive(ctx, id)
	}

	id, err := l.resolveRyuohInput(ctx, in)
	if err != nil {
		return Fetched{}, err
	}
	return l.fetchRyuoh(ctx, id)
}

// Refresh は取得済みの棋譜を取り直す（**保存はしない**）。
//
// 入力欄からの Fetch と違って**取得元が分かっている**ので、判別も
// 中継ページ → 棋譜 ID の往復も挟まらず、棋譜 ID で直接取りに行く。
//
// 保存は画面の内容をそのまま書き込む（Save はサイトへ取りに行かない）ので、
// **対局中に取ったものを後で保存するなら先にここを通す。**
// そうしないと取得した時点の古い棋譜が入る。
func (l *Library) Refresh(ctx context.Context, source, sourceID string) (Fetched, error) {
	if strings.TrimSpace(sourceID) == "" {
		return Fetched{}, fmt.Errorf("%w: 取得元の棋譜 ID が空です", ErrNoInput)
	}
	switch source {
	case store.SourceShogiLive:
		return l.fetchShogiLive(ctx, sourceID)
	case store.SourceYomiuri, "":
		return l.fetchRyuoh(ctx, sourceID)
	default:
		// url / paste は取得元での一意な ID を持たないので取り直せない。
		return Fetched{}, fmt.Errorf("%w: %s", ErrUnsupportedSource, source)
	}
}

// PreviewKIF は KIF テキストを解析して内容を返す（**保存はしない**）。
// 取り込み前に画面へ出すためのもので、ImportKIF と同じ判定を通る。
func (l *Library) PreviewKIF(text string) (Fetched, error) {
	doc, err := parseImportable(text)
	if err != nil {
		return Fetched{}, err
	}
	return fromDocument(doc, store.SourcePaste, "", text, EncodingUTF8), nil
}

// PreviewURL は URL から KIF を取得して解析する（**保存はしない**）。
func (l *Library) PreviewURL(ctx context.Context, rawURL string) (Fetched, error) {
	text, encoding, err := l.FetchKIFFromURL(ctx, rawURL)
	if err != nil {
		return Fetched{}, err
	}
	doc, err := parseImportable(text)
	if err != nil {
		return Fetched{}, err
	}
	return fromDocument(doc, store.SourceURL, rawURL, text, encoding), nil
}

// fetchRyuoh は読売のペイロードから KIF を組み立てる（保存はしない）。
func (l *Library) fetchRyuoh(ctx context.Context, id string) (Fetched, error) {
	g, err := l.yomiuri.FetchGame(ctx, id)
	if err != nil {
		return Fetched{}, err
	}
	return fromYomiuri(g), nil
}

// fetchShogiLive は連盟の中継から .kif を取る（保存はしない）。
func (l *Library) fetchShogiLive(ctx context.Context, id string) (Fetched, error) {
	g, err := l.shogilive.FetchGame(ctx, id)
	if err != nil {
		return Fetched{}, err
	}
	return l.fromShogiLive(g), nil
}

// resolveRyuohInput は入力（対局ページ URL / 棋譜ビューア URL / 棋譜 ID）を
// 読売の棋譜 ID に解決する。
func (l *Library) resolveRyuohInput(ctx context.Context, in string) (string, error) {
	if !strings.HasPrefix(in, "http://") && !strings.HasPrefix(in, "https://") {
		return in, nil // 棋譜 ID とみなす
	}
	// 棋譜ビューアの URL が直接貼られた場合はそこから ID を取る。
	if id, err := scrape.KifuIDFromViewerURL(in); err == nil {
		return id, nil
	}
	// 対局ページ URL → iframe を辿って棋譜 ID を得る。
	return l.yomiuri.FetchKifuID(ctx, in)
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
