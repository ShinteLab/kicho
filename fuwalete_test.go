package kicho

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// fetcherWithFuwalete はふわりと将棋の取得だけ記録用の transport に差し替えた Fetcher を返す。
func fetcherWithFuwalete() (*Fetcher, *recordingTransport) {
	rt := &recordingTransport{}
	f := NewFetcher()
	f.fuwalete = &scrape.Fuwalete{Client: &http.Client{Transport: rt}}
	return f, rt
}

const fuwaleteSlug = "ito-365549"

// 記事を REST API で取りに行ったか（記事の HTML ではなく）。
func wentToFuwalete(t *testing.T, rt *recordingTransport) {
	t.Helper()
	if len(rt.urls) != 1 || !strings.HasPrefix(rt.urls[0], "https://fuwalete.com/wp-json/wp/v2/posts?") ||
		!strings.Contains(rt.urls[0], "slug="+fuwaleteSlug) {
		t.Errorf("取りに行った先 = %v", rt.urls)
	}
}

// fuwalete.com の URL は**汎用の URL（.kif として読む）より先に**ふわりと将棋へ回すこと。
func TestFetchRoutesFuwalete(t *testing.T) {
	f, rt := fetcherWithFuwalete()
	_, err := f.Fetch(context.Background(), "https://fuwalete.com/"+fuwaleteSlug+"/")
	if err == nil || !strings.Contains(err.Error(), "ふわりと将棋") {
		t.Fatalf("err = %v, want ふわりと将棋のエラー", err)
	}
	wentToFuwalete(t, rt)

	// 記事でない URL は汎用の口へ落とさず、その場で理由を返す。
	_, err = f.Fetch(context.Background(), "https://fuwalete.com/category/x/")
	if err == nil || !strings.Contains(err.Error(), "記事の URL ではありません") {
		t.Errorf("err = %v", err)
	}
}

// Refresh は slug で直接取りに行く。
func TestRefreshFuwalete(t *testing.T) {
	f, rt := fetcherWithFuwalete()
	if _, err := f.Refresh(context.Background(), store.SourceFuwalete, fuwaleteSlug); err == nil {
		t.Fatal("404 なのに成功した")
	}
	wentToFuwalete(t, rt)
}

// 諸元は記事の URL。保存・追跡のどちらも取得元から決め直し、その URL から slug に戻せる。
func TestFuwaleteSourceURL(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	want := "https://fuwalete.com/" + fuwaleteSlug + "/"
	in := Fetched{Source: store.SourceFuwalete, SourceID: fuwaleteSlug, SourceURL: "https://example.com/でたらめ", KIF: importSample}

	rec, err := lib.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Source != store.SourceFuwalete || rec.SourceURL != want {
		t.Errorf("Save: source = %q, url = %q", rec.Source, rec.SourceURL)
	}

	w, err := lib.Watch(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if w.SourceURL != want {
		t.Errorf("Watch: url = %q", w.SourceURL)
	}

	if got := RefetchableURL(store.SourceFuwalete, want); got != want {
		t.Errorf("RefetchableURL = %q", got)
	}
	slug, err := scrape.FuwaleteSlug(want)
	if err != nil || slug != fuwaleteSlug {
		t.Errorf("諸元から slug に戻せない: %q, %v", slug, err)
	}
}
