package kicho

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ShinteLab/kicho/store"
)

// URL を「取得元での ID」にする正規化。
//
// ⚠️ **歯止めの中身は「同じ棋譜を指す URL が同じ ID になること」。** ここが
// 揺れると `(source, source_id)` の upsert に乗らず、**登録し直すたびに
// 棋譜が増える**（それが 2026-09-12 まで起きていたこと）。
// ⚠️ **パスとクエリを落とさないことも見ている** —— 落とすと別の棋譜が
// 同じ ID になりうる。
func TestSourceIDForURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"そのまま", "https://example.test/kifu/x.kif", "https://example.test/kifu/x.kif"},
		{"前後の空白は落とす", "  https://example.test/x.kif  ", "https://example.test/x.kif"},
		{"スキームとホストは小文字に", "HTTPS://Example.TEST/Kifu/X.kif", "https://example.test/Kifu/X.kif"},
		{"既定ポートは落とす", "https://example.test:443/x.kif", "https://example.test/x.kif"},
		{"既定でないポートは残す", "http://example.test:8080/x.kif", "http://example.test:8080/x.kif"},
		{"フラグメントは落とす", "https://example.test/x.kif#37", "https://example.test/x.kif"},
		{"クエリは残す", "https://example.test/kifu?id=7&x=1", "https://example.test/kifu?id=7&x=1"},
		{"パスの大文字小文字は残す", "https://example.test/A/B.kif", "https://example.test/A/B.kif"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceIDForURL(tt.in); got != tt.want {
				t.Errorf("sourceIDForURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// 連盟でも読売でもない URL は「その中身を .kif として読む」へ回ること。
//
// ⚠️ **ここが読売の解決へ落ちると**、他サイトの .kif の URL が
// 「読売の棋譜 ID」扱いになり意味の分からないエラーで落ちる。**取得元の判別を
// 呼び出し側に書かせないための入口**なので、この振り分けが命。
func TestFetchReadsPlainKifURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(importSample))
	}))
	defer srv.Close()

	// ⚠️ **DB を開かずに取得できること**（`NewFetcher`）。棚が開けなくても
	// URL の棋譜は解析できる、という利用側の約束の土台。
	got, err := NewFetcher().Fetch(context.Background(), srv.URL+"/kifu/x.kif")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != store.SourceURL {
		t.Errorf("Source = %q, want %q", got.Source, store.SourceURL)
	}
	if got.SourceID != srv.URL+"/kifu/x.kif" {
		t.Errorf("SourceID = %q, want the URL", got.SourceID)
	}
	if got.Event != "テスト棋戦" || got.Moves != 3 {
		t.Errorf("メタデータが取れていない: %+v", got)
	}
}

// 同じ URL を取り込み直しても増えないこと（upsert に乗る）。
//
// ⚠️ 登録のたびに UUID を振ると、同じ棋譜が何件も並び「更新」でも追えなくなる。
func TestImportURLIsIdempotent(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(importSample))
	}))
	defer srv.Close()

	first, err := lib.ImportURL(ctx, srv.URL+"/kifu/x.kif")
	if err != nil {
		t.Fatalf("1 回目: %v", err)
	}
	// 大文字小文字とフラグメントが違うだけの URL も同じ棋譜として扱うこと。
	second, err := lib.ImportURL(ctx, srv.URL+"/kifu/x.kif#5")
	if err != nil {
		t.Fatalf("2 回目: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("別の棋譜になっている: %q → %q", first.ID, second.ID)
	}
	n, err := lib.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Errorf("件数 = %d, want 1", n)
	}
}

// url も「更新」で取り直せること（sourceID がそのまま取得先）。
func TestRefreshURLRefetches(t *testing.T) {
	longer := importSample + "   4 ８五歩(84)\n"
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if hits == 1 {
			w.Write([]byte(importSample))
			return
		}
		w.Write([]byte(longer))
	}))
	defer srv.Close()

	f := NewFetcher()
	ctx := context.Background()
	first, err := f.Fetch(ctx, srv.URL+"/x.kif")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	again, err := f.Refresh(ctx, first.Source, first.SourceID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if again.KIF == first.KIF {
		t.Errorf("取り直していない（本文が変わっていない）")
	}
}
