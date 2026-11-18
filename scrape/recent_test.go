package scrape

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock はテスト用の時計。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newRecent[T any]() (*recent[T], *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
	return &recent[T]{now: c.now}, c
}

func TestRecentKeepsWithinInterval(t *testing.T) {
	r, clock := newRecent[int]()
	var calls int
	fetch := func(context.Context) (int, error) { calls++; return calls, nil }
	ctx := context.Background()

	if v, _ := r.do(ctx, MinFetchInterval, "a", fetch); v != 1 {
		t.Fatalf("1 回目 = %d", v)
	}
	clock.advance(MinFetchInterval - time.Second)
	if v, _ := r.do(ctx, MinFetchInterval, "a", fetch); v != 1 || calls != 1 {
		t.Fatalf("間隔の内側で取りに行った: v=%d calls=%d", v, calls)
	}
	// ID が違えば別。
	if v, _ := r.do(ctx, MinFetchInterval, "b", fetch); v != 2 {
		t.Fatalf("別の ID = %d", v)
	}
	clock.advance(time.Second)
	if v, _ := r.do(ctx, MinFetchInterval, "a", fetch); v != 3 {
		t.Fatalf("間隔を過ぎたのに取りに行かない: v=%d", v)
	}
}

func TestRecentZeroIntervalAlwaysFetches(t *testing.T) {
	r, _ := newRecent[int]()
	var calls int
	fetch := func(context.Context) (int, error) { calls++; return calls, nil }
	r.do(context.Background(), 0, "a", fetch)
	r.do(context.Background(), 0, "a", fetch)
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// サイトのエラーも持つ(落ちているサイトを叩き続けない)。
func TestRecentKeepsErrors(t *testing.T) {
	r, clock := newRecent[int]()
	var calls int
	boom := errors.New("HTTP 503")
	fetch := func(context.Context) (int, error) { calls++; return 0, boom }
	ctx := context.Background()

	r.do(ctx, MinFetchInterval, "a", fetch)
	if _, err := r.do(ctx, MinFetchInterval, "a", fetch); !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
	clock.advance(MinFetchInterval)
	r.do(ctx, MinFetchInterval, "a", fetch)
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// 呼び出し側の取り消しで終わった結果は持たない(次の呼び出しは取りに行ける)。
func TestRecentDoesNotKeepCallerCancel(t *testing.T) {
	r, _ := newRecent[int]()
	var calls int
	ctx, cancel := context.WithCancel(context.Background())
	_, err := r.do(ctx, MinFetchInterval, "a", func(ctx context.Context) (int, error) {
		calls++
		cancel()
		return 0, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	v, err := r.do(context.Background(), MinFetchInterval, "a", func(context.Context) (int, error) {
		calls++
		return 7, nil
	})
	if err != nil || v != 7 || calls != 2 {
		t.Fatalf("v = %d, err = %v, calls = %d", v, err, calls)
	}
}

// 同じ ID を同時に呼んでも、取りに行くのは 1 回。
func TestRecentConcurrentCallsShareOneFetch(t *testing.T) {
	r, _ := newRecent[int]()
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) (int, error) {
		calls.Add(1)
		<-release
		return 42, nil
	}

	var wg sync.WaitGroup
	results := make([]int, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = r.do(context.Background(), MinFetchInterval, "a", fetch)
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // 全員が待ちに入るまで
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	for i, v := range results {
		if v != 42 {
			t.Errorf("results[%d] = %d", i, v)
		}
	}
}

// 待っている間も ctx で抜けられる。
func TestRecentWaitHonorsContext(t *testing.T) {
	r, _ := newRecent[int]()
	release := make(chan struct{})
	defer close(release)
	go r.do(context.Background(), MinFetchInterval, "a", func(context.Context) (int, error) {
		<-release
		return 1, nil
	})
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.do(ctx, MinFetchInterval, "a", func(context.Context) (int, error) { return 2, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

// 期限を過ぎた結果は溜めない。
func TestRecentPrunesStaleEntries(t *testing.T) {
	r, clock := newRecent[int]()
	fetch := func(context.Context) (int, error) { return 1, nil }
	ctx := context.Background()
	r.do(ctx, MinFetchInterval, "a", fetch)
	r.do(ctx, MinFetchInterval, "b", fetch)
	clock.advance(MinFetchInterval)
	r.do(ctx, MinFetchInterval, "c", fetch)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) != 1 || r.entries["c"] == nil {
		t.Fatalf("entries = %v", r.entries)
	}
}

// 取得元に組み込んだ形: NewXxx の間隔の内側では、サイトへは 1 回しか行かない。
func TestFuwaleteMinInterval(t *testing.T) {
	var hits atomic.Int32
	body := readTestdata(t, "fuwalete_post_finished.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewFuwalete()
	s.Client, s.BaseURL = srv.Client(), srv.URL
	clock := &fakeClock{t: time.Now()}
	s.recent.now = clock.now
	ctx := context.Background()

	first, err := s.FetchGame(ctx, "ito-365549")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.FetchKifu(ctx, "ito-365549") // ライブ経路も同じ結果を使う
	if err != nil || again == "" {
		t.Fatalf("FetchKifu: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("間隔の内側でサイトへ %d 回行った", hits.Load())
	}
	clock.advance(MinFetchInterval)
	second, err := s.FetchGame(ctx, "ito-365549")
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || second == first {
		t.Fatalf("間隔を過ぎても取り直さない: hits=%d", hits.Load())
	}
}

// 構造体を直接作ると間隔は 0（毎回取りに行く）。
func TestZeroValueSourceAlwaysFetches(t *testing.T) {
	var hits atomic.Int32
	body := readTestdata(t, "fuwalete_post_finished.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := &Fuwalete{Client: srv.Client(), BaseURL: srv.URL}
	for range 2 {
		if _, err := s.FetchGame(context.Background(), "ito-365549"); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", hits.Load())
	}
}

func TestNewSourcesSetMinInterval(t *testing.T) {
	for name, got := range map[string]time.Duration{
		"yomiuri":   NewYomiuri().MinInterval,
		"shogilive": NewShogiLive().MinInterval,
		"shogidb2":  NewShogiDB2().MinInterval,
		"fuwalete":  NewFuwalete().MinInterval,
	} {
		if got != MinFetchInterval {
			t.Errorf("%s: MinInterval = %v", name, got)
		}
	}
}
