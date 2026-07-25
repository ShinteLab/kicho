// Package kicho は棋帳(棋譜の取得・保存・配信)のユースケースをまとめる。
//
// Wails には依存しない。デスクトップ側の Service はこの Library を呼ぶだけにし、
// Wails 依存を _cmd/kicho/ に閉じ込める。
package kicho

import (
	"context"
	"fmt"
	"log/slog"

	"shinte/kicho/format"
	"shinte/kicho/httpapi"
	"shinte/kicho/scrape"
	"shinte/kicho/settings"
	"shinte/kicho/store"
)

// Library は棋譜の取得・保存・配信をまとめたもの。
type Library struct {
	store   *store.Store
	yomiuri *scrape.Yomiuri
	server  *httpapi.Server
	logger  *slog.Logger
}

// Open は棋譜 DB を開いて Library を作る。dbPath が空なら既定の保存先を使う。
func Open(dbPath string, logger *slog.Logger) (*Library, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if dbPath == "" {
		p, err := settings.DatabasePath()
		if err != nil {
			return nil, err
		}
		dbPath = p
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	y := scrape.NewYomiuri()
	return &Library{
		store:   st,
		yomiuri: y,
		server:  httpapi.New(st, y, logger),
		logger:  logger,
	}, nil
}

// Close は DB を閉じ、HTTP サーバが動いていれば止める。
func (l *Library) Close(ctx context.Context) error {
	if err := l.server.Stop(ctx); err != nil {
		l.logger.Error("stop http server", "error", err)
	}
	return l.store.Close()
}

// Server は HTTP サーバを返す(起動・停止用)。
func (l *Library) Server() *httpapi.Server { return l.server }

// Store は棋譜 DB を返す。
func (l *Library) Store() *store.Store { return l.store }

// KifuIDFromURL は棋譜ビューアの URL(`.../kifu/s/{id}/`)から ID を取り出す。
// 対局ページ URL には使えない(そちらは ResolveRyuohURL)。
func KifuIDFromURL(raw string) (string, error) {
	return scrape.KifuIDFromViewerURL(raw)
}

// ResolveRyuohURL は読売の対局ページ URL から棋譜 ID を取り出す。
func (l *Library) ResolveRyuohURL(ctx context.Context, pageURL string) (string, error) {
	return l.yomiuri.FetchKifuID(ctx, pageURL)
}

// FetchRyuoh は棋譜 ID から取得する(保存はしない。プレビュー用)。
func (l *Library) FetchRyuoh(ctx context.Context, id string) (*scrape.Game, error) {
	return l.yomiuri.FetchGame(ctx, id)
}

// Save は取得済みの棋譜を保存する。
//
// 取得と保存は分けてある。対局中の棋譜は随時更新されるため、
// 「取得 → 内容を確認 → その表示内容を保存」という流れにしたいのと、
// 保存のたびにサイトへ取りに行かないようにするため
// (取得は /ryuoh/kifu のライブ経路、DB は終局後のアーカイブという役割分担)。
//
// 同じ棋譜を保存し直しても重複せず、既存の ID を維持したまま内容が更新される。
// 対局中に保存したものを終局後に取り直して保存すれば、同じ ID のまま最新になる。
func (l *Library) Save(ctx context.Context, g *scrape.Game) (store.Record, error) {
	if g == nil {
		return store.Record{}, fmt.Errorf("kicho: game is nil")
	}
	return l.store.Save(ctx, store.Game{
		Source:   store.SourceYomiuri,
		SourceID: g.SourceID,
		// 諸元: どこから取ったか。読売は棋譜ビューアの URL。
		SourceURL: scrape.ViewerURL(g.SourceID),
		Event:     g.Event,
		Handicap:  g.Handicap,
		Place:     g.Place,
		Black:     g.Black,
		White:     g.White,
		StartedAt: g.StartedAt,
		EndMark:   g.EndMark,
		Moves:     len(g.Moves),

		// スクレイピングは構造化データから組み立てるので、これ自体が原本。
		Body:     g.KIF(),
		Format:   string(format.KIF),
		Encoding: EncodingUTF8,
	})
}
