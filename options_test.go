package kicho

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// intervals は取得元ごとの間隔。取得元を足したらここにも足す（足し忘れを見つけるため）。
func intervals(f *Fetcher) map[string]time.Duration {
	return map[string]time.Duration{
		"yomiuri":   f.yomiuri.MinInterval,
		"shogilive": f.shogilive.MinInterval,
		"shogidb2":  f.shogidb2.MinInterval,
		"fuwalete":  f.fuwalete.MinInterval,
	}
}

func TestMinFetchIntervalOption(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{"既定は 10 秒", nil, 10 * time.Second},
		{"秒数を変える", []Option{WithMinFetchInterval(3 * time.Second)}, 3 * time.Second},
		{"0 は毎回取りに行く", []Option{WithMinFetchInterval(0)}, 0},
		{"負は 0 に寄せる", []Option{WithMinFetchInterval(-time.Second)}, 0},
		{"nil の Option は無視する", []Option{nil}, 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFetcher(tt.opts...)
			for src, got := range intervals(f) {
				if got != tt.want {
					t.Errorf("%s = %v, want %v", src, got, tt.want)
				}
			}
			if f.MinFetchInterval() != tt.want {
				t.Errorf("MinFetchInterval() = %v", f.MinFetchInterval())
			}
		})
	}
	if DefaultMinFetchInterval != 10*time.Second {
		t.Errorf("DefaultMinFetchInterval = %v", DefaultMinFetchInterval)
	}
}

// Open に渡した設定は Library の取得（とライブ経路が使う同じ取得元）に効く。
func TestOpenPassesOptions(t *testing.T) {
	lib, err := Open(filepath.Join(t.TempDir(), "kicho.db"), nil, WithMinFetchInterval(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close(context.Background())
	for src, got := range intervals(lib.Fetcher) {
		if got != 30*time.Second {
			t.Errorf("%s = %v, want 30s", src, got)
		}
	}
}
