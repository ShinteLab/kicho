package kicho

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// recordingTransport は来たリクエストを覚えて 404 を返す（外へは出ない）。
type recordingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.urls = append(rt.urls, r.URL.String())
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       http.NoBody,
		Header:     http.Header{},
		Request:    r,
	}, nil
}

// fetcherWithShogiDB2 は将棋DB2 の取得だけ記録用の transport に差し替えた Fetcher を返す。
func fetcherWithShogiDB2() (*Fetcher, *recordingTransport) {
	rt := &recordingTransport{}
	f := NewFetcher()
	f.shogidb2 = &scrape.ShogiDB2{Client: &http.Client{Transport: rt}}
	return f, rt
}

const db2ID = "3186d1e7f8f33242990b1e853f53250d443dbe25132936b005068009a201"

// shogidb2.com の URL は**汎用の URL（.kif として読む）より先に**将棋DB2 へ回すこと。
// 汎用の口に落ちると、HTML で .kif へのリンクも無いので「KIF ではない」で終わる。
func TestFetchRoutesShogiDB2(t *testing.T) {
	f, rt := fetcherWithShogiDB2()
	_, err := f.Fetch(context.Background(), "https://shogidb2.com/games/"+db2ID)
	if err == nil || !strings.Contains(err.Error(), "将棋DB2") {
		t.Fatalf("err = %v, want 将棋DB2 のエラー", err)
	}
	if len(rt.urls) != 1 || rt.urls[0] != "https://shogidb2.com/games/"+db2ID {
		t.Errorf("取りに行った先 = %v", rt.urls)
	}

	// 対局ページでない URL は汎用の口へ落とさず、その場で理由を返す。
	_, err = f.Fetch(context.Background(), "https://shogidb2.com/tournament/x")
	if err == nil || !strings.Contains(err.Error(), "/games/") {
		t.Errorf("err = %v", err)
	}
}

// Refresh は棋譜 ID で直接取りに行く（URL からの解決を挟まない）。
func TestRefreshShogiDB2(t *testing.T) {
	f, rt := fetcherWithShogiDB2()
	if _, err := f.Refresh(context.Background(), store.SourceShogiDB2, db2ID); err == nil {
		t.Fatal("404 なのに成功した")
	}
	if len(rt.urls) != 1 || rt.urls[0] != "https://shogidb2.com/games/"+db2ID {
		t.Errorf("取りに行った先 = %v", rt.urls)
	}
}

// 諸元は対局ページの URL。保存・追跡のどちらも取得元から決め直す（画面の値を信じない）。
// その URL を Fetch に渡せば取り直せるので、RefetchableURL もそのまま返す。
func TestShogiDB2SourceURL(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	want := "https://shogidb2.com/games/" + db2ID
	in := Fetched{Source: store.SourceShogiDB2, SourceID: db2ID, SourceURL: "https://example.com/でたらめ", KIF: importSample}

	rec, err := lib.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Source != store.SourceShogiDB2 || rec.SourceURL != want {
		t.Errorf("Save: source = %q, url = %q", rec.Source, rec.SourceURL)
	}

	w, err := lib.Watch(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if w.SourceURL != want {
		t.Errorf("Watch: url = %q", w.SourceURL)
	}

	if got := RefetchableURL(store.SourceShogiDB2, want); got != want {
		t.Errorf("RefetchableURL = %q", got)
	}
	id, err := scrape.ShogiDB2GameID(want)
	if err != nil || id != db2ID {
		t.Errorf("諸元から棋譜 ID に戻せない: %q, %v", id, err)
	}
}
