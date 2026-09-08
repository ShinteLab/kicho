package kicho

import (
	"context"
	"fmt"
	"strings"

	"github.com/ShinteLab/kicho/store"
)

// 追跡中の中継（UI の「仮の一覧」）を触る口。
//
// **サイトを一時的に覚えておくためのもので、棋譜そのものは持たない。**
// 2日制の対局では翌日また中継の URL を貼り直すことになるので、
// 「どのサイトのどの棋譜か」を再起動を跨いで残せるようにしてある。
// 一覧で見分けられるよう棋戦名・対局者・手数も一緒に持つが、
// **これは表示用の写しであって原本ではない**（原本は取り直せば取れる）。
//
// 棋譜そのものを残すのは Save（蔵書）の役目。役割を混ぜないこと。

// Watches は追跡中の中継を新しい順に返す。
//
// 復元してもここからサイトへは取りに行かない（棋譜本文は入っていない）。
// 最新の棋譜が要るときは Refresh を通す。
func (l *Library) Watches(ctx context.Context) ([]store.Watch, error) {
	return l.store.Watches(ctx)
}

// Watch は取得結果を追跡一覧に載せる（既にあれば内容を最新化する）。
//
// ⚠️ **ライブ中継（読売・連盟）だけを載せる。** url / paste は取得元での
// 一意な ID を持たず sourceId が毎回新しい UUID になるため、そこへ取り直しに
// 行けない —— 復元しても「更新」が必ず失敗するカードになる。
//
// 諸元(source_url)は Save と同じく取得元から決め直す（画面の値を信じない）。
func (l *Library) Watch(ctx context.Context, f Fetched) (store.Watch, error) {
	if strings.TrimSpace(f.SourceID) == "" {
		return store.Watch{}, fmt.Errorf("%w: 取得元の棋譜 ID が空です", ErrNoInput)
	}

	source, sourceURL, err := l.resolveSource(f.Source, f.SourceID, f.SourceURL)
	if err != nil {
		return store.Watch{}, err
	}
	if source != store.SourceYomiuri && source != store.SourceShogiLive {
		return store.Watch{}, fmt.Errorf("%w: %s は取り直せないので追跡できません",
			ErrUnsupportedSource, source)
	}

	return l.store.SaveWatch(ctx, store.Watch{
		Source:    source,
		SourceID:  f.SourceID,
		SourceURL: sourceURL,
		Event:     f.Event,
		Black:     f.Black,
		White:     f.White,
		StartedAt: f.StartedAt,
		EndMark:   f.EndMark,
		Moves:     f.Moves,
	})
}

// Unwatch は追跡をやめる。無ければ store.ErrNotFound。
//
// **保存済みの棋譜は消えない。** 仮の一覧から外すだけ。
func (l *Library) Unwatch(ctx context.Context, source, sourceID string) error {
	return l.store.DeleteWatch(ctx, source, sourceID)
}

// UnwatchAll は追跡をすべてやめる（UI の「クリア」）。消した件数を返す。
func (l *Library) UnwatchAll(ctx context.Context) (int, error) {
	return l.store.DeleteWatches(ctx)
}
