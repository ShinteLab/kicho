package store

import (
	"context"
	"testing"
	"time"
)

// seedGames は検索テスト用の棋譜を入れる。
func seedGames(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

	games := []Game{
		{
			Source: SourceYomiuri, SourceID: "g1",
			Event: "第37期竜王戦七番勝負第１局",
			Black: "藤井聡太竜王", White: "佐々木勇気八段",
			Place:     "東京都",
			StartedAt: time.Date(2024, 10, 5, 9, 0, 0, 0, time.UTC),
			EndMark:   "投了", Moves: 118, Body: "手合割：平手\n1 ２六歩(27)\n",
		},
		{
			Source: SourceYomiuri, SourceID: "g2",
			Event: "第37期竜王戦七番勝負第２局",
			Black: "佐々木勇気八段", White: "藤井聡太竜王",
			Place:     "福井県あわら市",
			StartedAt: time.Date(2024, 10, 19, 9, 0, 0, 0, time.UTC),
			EndMark:   "投了", Moves: 104, Body: "手合割：平手\n1 ７六歩(77)\n",
		},
		{
			Source: SourceYomiuri, SourceID: "g3",
			Event: "第39期竜王戦１組ランキング戦",
			Black: "永瀬拓矢九段", White: "伊藤匠二冠",
			Place:     "大阪府",
			StartedAt: time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
			EndMark:   "", Moves: 60, Body: "手合割：平手\n1 ７六歩(77)\n", // 対局中
		},
	}
	for _, g := range games {
		if _, err := s.Save(ctx, g); err != nil {
			t.Fatalf("Save %s: %v", g.SourceID, err)
		}
	}
}

func ids(recs []Record) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.SourceID)
	}
	return out
}

func TestSearchNoConditionsReturnsAllNewestFirst(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	got, err := s.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"g3", "g2", "g1"} // 開始日時の新しい順
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%v)", len(got), ids(got))
	}
	for i := range want {
		if got[i].SourceID != want[i] {
			t.Errorf("order = %v, want %v", ids(got), want)
			break
		}
	}
	// 検索結果は KIF 本文を含まない(一覧用)。
	if got[0].Body != "" {
		t.Errorf("Search should omit the KIF body, got %q", got[0].Body)
	}
}

// 3文字以上は FTS5(trigram) で中間一致する。
func TestSearchTextUsesTrigram(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	cases := map[string][]string{
		"藤井聡太":  {"g2", "g1"}, // 対局者(先手・後手どちらでも)
		"七番勝負":  {"g2", "g1"}, // 棋戦名の中間
		"ランキング": {"g3"},
		"あわら市":  {"g2"}, // 場所
		"第39期":  {"g3"},
	}
	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			got, err := s.Search(ctx, Query{Text: text})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("Search(%q) = %v, want %v", text, ids(got), want)
			}
			for i := range want {
				if got[i].SourceID != want[i] {
					t.Errorf("Search(%q) = %v, want %v", text, ids(got), want)
					break
				}
			}
		})
	}
}

// 3文字未満は trigram が使えないので LIKE にフォールバックする。
func TestSearchShortTextFallsBackToLike(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	got, err := s.Search(context.Background(), Query{Text: "藤井"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("Search(\"藤井\") = %v, want g2 and g1", ids(got))
	}
}

func TestSearchNoMatch(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	got, err := s.Search(context.Background(), Query{Text: "存在しない棋戦"})
	if err != nil {
		t.Fatal(err)
	}
	// nil ではなく空スライスを返す(呼び出し側で扱いやすい)。
	if got == nil {
		t.Fatal("Search should return an empty slice, not nil")
	}
	if len(got) != 0 {
		t.Errorf("= %v, want empty", ids(got))
	}
}

// LIKE のワイルドカードを検索語として扱えること(索引を壊さないこと)。
func TestSearchEscapesLikeWildcards(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	got, err := s.Search(context.Background(), Query{Text: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("'%%' should be a literal, matched %v", ids(got))
	}
}

// FTS のクエリ構文を検索語として扱えること(構文エラーにならないこと)。
func TestSearchEscapesFTSSyntax(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	for _, text := range []string{`"竜王戦"`, `竜王戦 OR 藤井`, `NEAR(藤井 竜王)`, `a"b"c`} {
		if _, err := s.Search(context.Background(), Query{Text: text}); err != nil {
			t.Errorf("Search(%q) error: %v", text, err)
		}
	}
}

func TestSearchDateRange(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	got, err := s.Search(ctx, Query{
		From: time.Date(2024, 10, 10, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "g2" {
		t.Errorf("= %v, want [g2]", ids(got))
	}

	// From だけ
	got, err = s.Search(ctx, Query{From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "g3" {
		t.Errorf("= %v, want [g3]", ids(got))
	}
}

// 範囲の境界は両端を含む。
func TestSearchDateRangeIsInclusive(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	at := time.Date(2024, 10, 19, 9, 0, 0, 0, time.UTC)
	got, err := s.Search(context.Background(), Query{From: at, To: at})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "g2" {
		t.Errorf("= %v, want [g2]", ids(got))
	}
}

func TestSearchFinishedOnly(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	got, err := s.Search(context.Background(), Query{FinishedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("= %v, want the two finished games", ids(got))
	}
	for _, r := range got {
		if !r.Finished() {
			t.Errorf("%s is not finished", r.SourceID)
		}
	}
}

func TestSearchCombinedConditions(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)

	got, err := s.Search(context.Background(), Query{
		Text:         "竜王戦",
		From:         time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		To:           time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		FinishedOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"g2", "g1"}
	if len(got) != 2 || got[0].SourceID != want[0] || got[1].SourceID != want[1] {
		t.Errorf("= %v, want %v", ids(got), want)
	}
}

func TestSearchLimitOffset(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	got, err := s.Search(ctx, Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SourceID != "g3" {
		t.Errorf("= %v, want the first two", ids(got))
	}

	got, err = s.Search(ctx, Query{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "g1" {
		t.Errorf("= %v, want [g1]", ids(got))
	}
}

// 保存し直したら FTS も追随すること(トリガが効いていること)。
func TestSearchReflectsUpdates(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	if got, _ := s.Search(ctx, Query{Text: "永瀬拓矢"}); len(got) != 1 {
		t.Fatalf("precondition failed: %v", ids(got))
	}

	// 対局者を差し替えて保存し直す。
	updated := Game{
		Source: SourceYomiuri, SourceID: "g3",
		Event: "第39期竜王戦１組ランキング戦",
		Black: "渡辺明九段", White: "伊藤匠二冠",
		StartedAt: time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
		Moves:     60, Body: "手合割：平手\n",
	}
	if _, err := s.Save(ctx, updated); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Search(ctx, Query{Text: "永瀬拓矢"}); len(got) != 0 {
		t.Errorf("stale FTS entry remains: %v", ids(got))
	}
	if got, _ := s.Search(ctx, Query{Text: "渡辺明九段"}); len(got) != 1 {
		t.Errorf("updated FTS entry missing: %v", ids(got))
	}
}

// 削除したら FTS からも消えること。
func TestSearchReflectsDeletes(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	rec, err := s.FindBySource(ctx, SourceYomiuri, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.Search(ctx, Query{Text: "七番勝負"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "g2" {
		t.Errorf("= %v, want only g2", ids(got))
	}
}

// 削除で KIF 本文も消えること(ON DELETE CASCADE)。
func TestDeleteRemovesKifu(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	rec, err := s.FindBySource(ctx, SourceYomiuri, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM game_kifu WHERE game_id = ?`, rec.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("game_kifu still has %d row(s) for the deleted game", n)
	}
}

// CountQuery と Search が同じ条件を見ていること。
//
// **whereClause を共有している意味がここ。** 片方だけ条件を足すと
// 「一覧に出る件数」と「該当件数」が食い違い、UI の「N 件中 M 件」が嘘になる。
func TestCountQueryAgreesWithSearch(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	queries := []Query{
		{},
		{Text: "竜王戦"},
		{Text: "決勝"}, // 3 文字未満ではないが FTS に当たらない語
		{Text: "王"},  // LIKE へフォールバックする長さ
		{FinishedOnly: true},
		{From: time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, q := range queries {
		got, err := s.Search(ctx, q)
		if err != nil {
			t.Fatalf("Search(%+v): %v", q, err)
		}
		n, err := s.CountQuery(ctx, q)
		if err != nil {
			t.Fatalf("CountQuery(%+v): %v", q, err)
		}
		if n != len(got) {
			t.Errorf("Query%+v: CountQuery = %d, Search = %d 件", q, n, len(got))
		}
	}
}

// Limit を掛けても CountQuery は該当件数を返すこと（切る前の数）。
func TestCountQueryIgnoresLimit(t *testing.T) {
	s := newTestStore(t)
	seedGames(t, s)
	ctx := context.Background()

	all, err := s.Search(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("テストデータが足りない: %d 件", len(all))
	}

	q := Query{Limit: 1}
	got, err := s.Search(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Search = %d 件, want 1", len(got))
	}
	n, err := s.CountQuery(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(all) {
		t.Errorf("CountQuery = %d, want %d（Limit を無視するはず）", n, len(all))
	}
}
