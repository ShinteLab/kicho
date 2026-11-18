package kicho

import (
	"time"

	"github.com/ShinteLab/kicho/scrape"
)

// Option は `Open` / `NewFetcher` の設定。渡さなければ既定のまま。
type Option func(*options)

type options struct {
	minFetchInterval time.Duration
}

func defaultOptions() options {
	return options{minFetchInterval: scrape.MinFetchInterval}
}

func applyOptions(opts []Option) options {
	o := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// DefaultMinFetchInterval は同じ棋譜を中継サイトへ取りに行く間隔の既定（10 秒）。
const DefaultMinFetchInterval = scrape.MinFetchInterval

// WithMinFetchInterval は、同じ棋譜を中継サイトへ取りに行く最短の間隔を決める（既定 10 秒）。
//
// 間隔の内に同じ棋譜を頼まれたら、サイトへは行かず前の結果（エラーも）を返す。
// 取得・更新のボタン、HTTP のライブ経路、`Fetch` / `Refresh` のどれにも効く。
// **0 以下にすると毎回取りに行く**（制限なし）。
//
// ⚠️ 間隔は Fetcher（Library）ごとに数える。同じ棋譜を別のプロセスや別の Fetcher からも
// 取るなら、そちらの分は別に数えられる。
func WithMinFetchInterval(d time.Duration) Option {
	return func(o *options) {
		if d < 0 {
			d = 0
		}
		o.minFetchInterval = d
	}
}
