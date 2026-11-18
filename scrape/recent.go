package scrape

import (
	"context"
	"sync"
	"time"
)

// MinFetchInterval は同じ棋譜をサイトへ取りに行く最短の間隔。
//
// **同じ棋譜は 10 秒に 1 回までしか取りに行かない。** 外部ツールがライブ経路
// (`/fuwalete/kifu/{id}` など)を間違って短い間隔で読み続けても、「更新」を
// 連打されても、サイトへのアクセスが増えないようにするため(2026-11-18)。
// 中継サイトの更新は 15 秒程度なので、10 秒以内に前の結果を返しても失うものは無い。
//
// 取得元の構造体の `MinInterval` に入る(`NewXxx` が入れる)。
const MinFetchInterval = 10 * time.Second

// recent は棋譜 ID ごとに「直近の取得結果」を持ち、間隔の内側ならそれを返す。
//
// ゼロ値で使える。持つのは:
//
//   - **値もエラーも持つ。** サイトが落ちているときに叩き続けないため
//   - ⚠️ **呼び出し側の ctx の取り消し・期限切れで終わった結果は持たないこと。**
//     それはサイトの状態ではなく呼び出し側の事情なので、次の呼び出しは取りに行けなければならない
//   - 同じ ID を同時に呼ばれたら、取りに行くのは 1 回だけ(後から来たほうは待って同じ結果を受け取る)
//
// ⚠️ **返す値(ポインタ)は間隔の内側の呼び出し全員で共有される。** 受け取った側は
// 中身を書き換えないこと。
type recent[T any] struct {
	mu      sync.Mutex
	entries map[string]*recentEntry[T]
	// now は時計(テストで差し替える)。nil なら time.Now。
	now func() time.Time
}

type recentEntry[T any] struct {
	// sem はこの ID の取得を 1 本に絞る(容量 1)。待つ間も ctx で抜けられるようにチャネルにしてある。
	sem chan struct{}
	// at は結果を得た時刻。ゼロなら結果を持っていない。
	at  time.Time
	val T
	err error
}

func (r *recent[T]) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// do は key の結果が interval より新しければそれを返し、古ければ fetch で取りに行く。
// interval が 0 以下なら毎回 fetch する(持たない)。
func (r *recent[T]) do(ctx context.Context, interval time.Duration, key string, fetch func(context.Context) (T, error)) (T, error) {
	if interval <= 0 {
		return fetch(ctx)
	}

	r.mu.Lock()
	if r.entries == nil {
		r.entries = map[string]*recentEntry[T]{}
	}
	e := r.entries[key]
	if e == nil {
		e = &recentEntry[T]{sem: make(chan struct{}, 1)}
		r.entries[key] = e
	}
	r.mu.Unlock()

	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	defer func() { <-e.sem }()

	if !e.at.IsZero() && r.clock().Sub(e.at) < interval {
		return e.val, e.err
	}

	v, err := fetch(ctx)
	if err != nil && ctx.Err() != nil {
		// 呼び出し側の取り消し・期限切れ。サイトの状態ではないので持たない。
		return v, err
	}
	e.at, e.val, e.err = r.clock(), v, err
	r.prune(interval, e)
	return v, err
}

// prune は間隔を過ぎた結果と、結果を持たないまま残った枠(取り消された取得)を捨てる
// (取得したことのある ID が溜まり続けないように)。取得中のものと keep は残す。
func (r *recent[T]) prune(interval time.Duration, keep *recentEntry[T]) {
	now := r.clock()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, e := range r.entries {
		if e == keep {
			continue
		}
		// at は e.sem を持っている間に書かれる。持てなければ取得中なので触らない。
		select {
		case e.sem <- struct{}{}:
			if e.at.IsZero() || now.Sub(e.at) >= interval {
				delete(r.entries, k)
			}
			<-e.sem
		default:
		}
	}
}
