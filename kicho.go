// Package kicho は棋帳(棋譜の取得・保存・配信)のユースケースをまとめる。
//
// Wails には依存しない。デスクトップ側の Service はこの Library を呼ぶだけにし、
// Wails 依存を _cmd/kicho/ に閉じ込める。
package kicho

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ShinteLab/kicho/format"
	"github.com/ShinteLab/kicho/httpapi"
	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/settings"
	"github.com/ShinteLab/kicho/store"
)

// Library は棋譜の取得・保存・配信をまとめたもの。
//
// ⚠️ **取得は `Fetcher` に切り出して埋め込んである**（2026-09-12）。
// `Library.Fetch` などは今までどおり呼べるが、**DB が要らない取得だけを
// したい側は `NewFetcher()` を使える**（詳しくは `Fetcher`）。
type Library struct {
	*Fetcher
	store  *store.Store
	server *httpapi.Server
	logger *slog.Logger
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
	f := NewFetcher()
	return &Library{
		Fetcher: f,
		store:   st,
		server:  httpapi.New(st, f.yomiuri, f.shogilive, logger).WithShogiDB2(f.shogidb2),
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

// 蔵書（保存済みの棋譜）を触る口。
//
// ⚠️ **`Store()` は公開していない。** 以前は `*store.Store` をそのまま返しており、
// kicho の Wails サービスも ikkyoku も蔵書操作をそこから直接呼んでいた。
// 実質の公開 API が `store` パッケージ全体になり、保存の組み立て（取得元ごとの
// source_url、文字コードの既定、手数の数え方）が呼び出し側に散った。
// **蔵書の操作はここに足すこと。**

// Count は保存件数を返す。
func (l *Library) Count(ctx context.Context) (int, error) {
	return l.store.Count(ctx)
}

// Get は棋譜1件を KIF 本文つきで返す。無ければ store.ErrNotFound。
func (l *Library) Get(ctx context.Context, id string) (store.Record, error) {
	return l.store.Get(ctx, id)
}

// Delete は棋譜を削除する。無ければ store.ErrNotFound。
func (l *Library) Delete(ctx context.Context, id string) error {
	return l.store.Delete(ctx, id)
}

// MaxSearchRows は Search が1回に返す上限。
//
// **棋譜を溜め込んでいく前提**なので、UI の一覧が件数無制限だと蔵書が増えた
// ぶんだけ全行が JSON に載る。`store.Search` の Limit 0（無制限）をアプリの
// 一覧にそのまま使わせないための蓋で、超えた分は SearchResult.Truncated で伝える。
//
// httpapi の一覧（`GET /kifu`）はここを通らない。外部ツールは全件を期待するため。
const MaxSearchRows = 500

// SearchResult は検索結果と件数。
//
// **件数を2つ返すのは UI が「N 件中 M 件」を出すため。** 検索のたびに
// 呼び出し側が Search と Count を別々に投げると、条件付きの該当件数が
// 取れず「絞り込んだ結果が全体の何件か」を出せない。
type SearchResult struct {
	// Games は条件に合う棋譜（新しい順、KIF 本文なし）。
	Games []store.Record
	// Matched は条件に合う件数（MaxSearchRows で切る前）。
	Matched int
	// Total は蔵書全体の件数（条件なし）。
	Total int
	// Truncated は上限で切ったかどうか。UI はここで「先頭 N 件」と出す。
	Truncated bool
}

// Search は条件に合う棋譜を新しい順に返す（KIF 本文は含まない）。
//
// Limit が 0 か MaxSearchRows を超えていれば MaxSearchRows に丸める。
func (l *Library) Search(ctx context.Context, q store.Query) (SearchResult, error) {
	if q.Limit <= 0 || q.Limit > MaxSearchRows {
		q.Limit = MaxSearchRows
	}

	games, err := l.store.Search(ctx, q)
	if err != nil {
		return SearchResult{}, err
	}
	matched, err := l.store.CountQuery(ctx, q)
	if err != nil {
		return SearchResult{}, err
	}
	total, err := l.store.Count(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{
		Games:     games,
		Matched:   matched,
		Total:     total,
		Truncated: matched > q.Offset+len(games),
	}, nil
}

// Save は取得済みの棋譜（画面に出している内容）を保存する。
//
// **取得と保存は分けてある。** 対局中の棋譜は随時更新されるため、
// 「取得 → 内容を確認 → その表示内容を保存」という流れにしたいのと、
// 保存のたびにサイトへ取りに行かないようにするため
// （取得は /ryuoh/kifu のライブ経路、DB は終局後のアーカイブという役割分担）。
// **ここからサイトへは取りに行かない。**
//
// 同じ棋譜を保存し直しても重複せず、既存の ID と created_at を維持したまま
// 内容が更新される。対局中に保存したものを終局後に取り直して保存すれば、
// 同じ ID のまま最新になる。
//
// ⚠️ **諸元（source_url）は取得元から決め直す。** Fetched は UI を往復して
// くるので、画面が持っている値をそのまま信じない。
func (l *Library) Save(ctx context.Context, f Fetched) (store.Record, error) {
	if strings.TrimSpace(f.SourceID) == "" {
		return store.Record{}, fmt.Errorf("%w: 取得元の棋譜 ID が空です", ErrNoInput)
	}
	if f.Empty() {
		return store.Record{}, ErrEmptyKifu
	}

	source, sourceURL, err := l.resolveSource(f.Source, f.SourceID, f.SourceURL)
	if err != nil {
		return store.Record{}, err
	}

	kifFormat := f.Format
	if kifFormat == "" {
		kifFormat = string(format.KIF)
	}
	encoding := f.Encoding
	if encoding == "" {
		encoding = EncodingUTF8
	}

	// 手数は本文から数え直す。**画面が持っている値を信じない**
	// （Fetched は UI を往復してくる。手数は本文から決まる派生値であって
	// 「原本をそのまま保存する」対象ではない）。
	moves := f.Moves
	if kifFormat == string(format.KIF) {
		moves = countMoves(f.KIF)
	}

	rec, err := l.store.Save(ctx, store.Game{
		Source:    source,
		SourceID:  f.SourceID,
		SourceURL: sourceURL,
		Event:     f.Event,
		Handicap:  f.Handicap,
		Place:     f.Place,
		Black:     f.Black,
		White:     f.White,
		StartedAt: f.StartedAt,
		EndMark:   f.EndMark,
		Moves:     moves,

		Body:     f.KIF,
		Format:   kifFormat,
		Encoding: encoding,
	})
	if err != nil {
		return store.Record{}, err
	}

	// 終局していれば追跡をやめる（もう取り直す必要がないため）。
	// **対局中の保存では外さない** —— 2日制なら翌日も同じカードで追うので、
	// 1日目の封じ手時点で保存したからといって一覧から消えては困る。
	if rec.Finished() {
		if err := l.Unwatch(ctx, rec.Source, rec.SourceID); err != nil &&
			!errors.Is(err, store.ErrNotFound) {
			// 保存自体は済んでいるので失敗にはしない（一覧に残るだけ）。
			l.logger.Warn("unwatch after save", "source", rec.Source,
				"sourceID", rec.SourceID, "error", err)
		}
	}
	return rec, nil
}

// resolveSource は取得元と諸元(source_url)を決め直す。
//
// **Fetched は UI を往復してくるので、画面が持っている source_url を信じない。**
// 保存（Save）と追跡（Watch）の両方が通るので、取得元ごとの決め方は
// ここ 1 か所だけに置く。
func (l *Library) resolveSource(source, sourceID, sourceURL string) (string, string, error) {
	switch source {
	case store.SourceShogiLive:
		return source, l.shogilive.ViewerURL(sourceID), nil
	case store.SourceShogiDB2:
		return source, l.shogidb2.GameURL(sourceID), nil
	case store.SourceYomiuri, "":
		// 取得元が入っていない古い画面状態でも読売として扱えるようにしておく。
		return store.SourceYomiuri, scrape.ViewerURL(sourceID), nil
	case store.SourceURL:
		// 貼られた URL をそのまま残す。⚠️ **空なら sourceID で補う** ——
		// url の sourceID は URL そのものなので（`sourceIDForURL`）、
		// 画面から諸元が落ちてきても諸元を失わない。
		if strings.TrimSpace(sourceURL) == "" {
			return source, sourceID, nil
		}
		return source, sourceURL, nil
	case store.SourcePaste:
		// 貼り付けは取得元が無いので空のまま。
		return source, sourceURL, nil
	default:
		return "", "", fmt.Errorf("%w: %s", ErrUnsupportedSource, source)
	}
}
