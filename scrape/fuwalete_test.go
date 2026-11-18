package scrape

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// testdata は実サイトの REST API の応答そのまま(2026-11-18 に取得)。
//
//	fuwalete_post_finished.json … 伊藤–永瀬戦(王将戦 挑決L。投了まで。74 手目に誤記がある)
//	fuwalete_post_live.json     … 豊島–広瀬戦(順位戦 A級。対局中。記事に 37 手)
//	fuwalete_live_page.json     … 対局中の記事が読み込む枠(/q22b/)。見出しだけで手はまだ無い

// fuwaleteServer は REST API の posts / pages を返すテスト用サーバを立てる。
// pages は slug → 応答(JSON の配列)。
func fuwaleteServer(t *testing.T, posts, pages map[string]string) *Fuwalete {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := r.URL.Query().Get("slug")
		var src map[string]string
		switch r.URL.Path {
		case "/wp-json/wp/v2/posts":
			src = posts
		case "/wp-json/wp/v2/pages":
			src = pages
		default:
			http.NotFound(w, r)
			return
		}
		body, ok := src[slug]
		if !ok {
			body = "[]"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Fuwalete{Client: srv.Client(), BaseURL: srv.URL}
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// editContent は応答の本文(content.rendered)を書き換える。
func editContent(t *testing.T, body string, edit func(string) string) string {
	t.Helper()
	var posts []map[string]any
	if err := json.Unmarshal([]byte(body), &posts); err != nil {
		t.Fatal(err)
	}
	c := posts[0]["content"].(map[string]any)
	before := c["rendered"].(string)
	after := edit(before)
	if after == before {
		t.Fatal("本文が書き換わっていない")
	}
	c["rendered"] = after
	b, err := json.Marshal(posts)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 誤記の手で止まり、その前までと、どこで止まったかを返す。推測で直さない。
func TestFuwaleteStopsAtTypo(t *testing.T) {
	s := fuwaleteServer(t, map[string]string{"ito-365549": readTestdata(t, "fuwalete_post_finished.json")}, nil)
	g, err := s.FetchGame(context.Background(), "ito-365549")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Moves) != 73 {
		t.Fatalf("手数 = %d, want 73", len(g.Moves))
	}
	if g.Finished() {
		t.Error("途中で止まったのに終局扱い")
	}
	if !strings.Contains(g.Note, "74 手目「△89飛」") {
		t.Errorf("Note = %q", g.Note)
	}
	if kif := g.KIF(); !strings.Contains(kif, "*ふわりと将棋の 74 手目") {
		t.Errorf("KIF の先頭に止まった理由が無い:\n%s", kif[:300])
	}
}

func TestFuwaleteFinishedGame(t *testing.T) {
	// 74 手目の誤記を直した版(△89飛 → △29飛)。8九には先手の桂がいて、76 手目の
	// △89飛成 は 2九から来ている。
	body := editContent(t, readTestdata(t, "fuwalete_post_finished.json"), func(c string) string {
		return strings.Replace(c, "74手　86%　△89飛", "74手　86%　△29飛", 1)
	})
	s := fuwaleteServer(t, map[string]string{"ito-365549": body}, nil)
	g, err := s.FetchGame(context.Background(), "ito-365549")
	if err != nil {
		t.Fatal(err)
	}

	if g.SourceID != "ito-365549" || g.Event != "王将戦 挑決L" {
		t.Errorf("SourceID = %q, Event = %q", g.SourceID, g.Event)
	}
	// 見出しは「後手△伊藤叡王　先手▲永瀬九段」。題のフルネームに直す。
	if g.Black != "永瀬拓矢" || g.White != "伊藤匠" {
		t.Errorf("先手 = %q, 後手 = %q", g.Black, g.White)
	}
	if want := time.Date(2026, 11, 6, 10, 0, 0, 0, jst); !g.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", g.StartedAt, want)
	}
	if g.TimeLimit != 4*time.Hour {
		t.Errorf("TimeLimit = %v", g.TimeLimit)
	}
	if g.Note != "" {
		t.Errorf("Note = %q", g.Note)
	}
	if !g.Finished() || g.EndMark != "投了" || len(g.Moves) != 98 {
		t.Fatalf("EndMark = %q, 手数 = %d", g.EndMark, len(g.Moves))
	}

	kif := g.KIF()
	for _, line := range []string{
		"1 ２六歩(27)",
		"10 ７七角成(22)",
		"11 同　銀(88)",
		"74 ２九飛打(00)",
		"76 ８九飛成(29)",
		"87 ５三銀成(62)",
		"97 ５五金打(00)",
		"98 投了",
		"*先手勝勢　寄せ合いで先手が速い", // 70 手目の解説
	} {
		if !strings.Contains(kif, line) {
			t.Errorf("KIF に %q が無い", line)
		}
	}
	// 「ーー 午後 開始 ーー」のような区切りは解説にしない。
	if strings.Contains(kif, "ーー") {
		t.Error("区切りの行が解説に入っている")
	}
}

func TestFuwaleteLiveGame(t *testing.T) {
	post := readTestdata(t, "fuwalete_post_live.json")
	livePage := readTestdata(t, "fuwalete_live_page.json")

	t.Run("記事の手と枠の見出し", func(t *testing.T) {
		s := fuwaleteServer(t, map[string]string{"toyo-367599": post}, map[string]string{"q22b": livePage})
		g, err := s.FetchGame(context.Background(), "toyo-367599")
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Moves) != 37 || g.Finished() || g.Note != "" {
			t.Fatalf("手数 = %d, EndMark = %q, Note = %q", len(g.Moves), g.EndMark, g.Note)
		}
		if g.Black != "豊島将之" || g.White != "広瀬章人" || g.Event != "順位戦 A級" {
			t.Errorf("先手 = %q, 後手 = %q, 棋戦 = %q", g.Black, g.White, g.Event)
		}
		if g.TimeLimit != 6*time.Hour {
			t.Errorf("TimeLimit = %v", g.TimeLimit)
		}
	})

	t.Run("枠の新しい手を足す", func(t *testing.T) {
		page := editContent(t, livePage, func(c string) string {
			return c + "<p class=\"wp-block-paragraph\">38手　50%　△44歩</p>"
		})
		s := fuwaleteServer(t, map[string]string{"toyo-367599": post}, map[string]string{"q22b": page})
		g, err := s.FetchGame(context.Background(), "toyo-367599")
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Moves) != 38 || g.Moves[37].Name != "４四歩" || g.Moves[37].FromX != 4 || g.Moves[37].FromY != 3 {
			t.Fatalf("手数 = %d, 最後 = %+v", len(g.Moves), g.Moves[len(g.Moves)-1])
		}
	})

	t.Run("枠が別の対局なら使わない", func(t *testing.T) {
		page := editContent(t, livePage, func(c string) string {
			c = strings.Replace(c, "後手△広瀬九段　先手▲豊島九段", "後手△羽生九段　先手▲石井七段", 1)
			return c + "<p class=\"wp-block-paragraph\">38手　50%　△44歩</p>"
		})
		s := fuwaleteServer(t, map[string]string{"toyo-367599": post}, map[string]string{"q22b": page})
		g, err := s.FetchGame(context.Background(), "toyo-367599")
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Moves) != 37 {
			t.Fatalf("手数 = %d, want 37", len(g.Moves))
		}
	})
}

func TestFuwaleteNotFound(t *testing.T) {
	s := fuwaleteServer(t, nil, nil)
	if _, err := s.FetchGame(context.Background(), "nothing-1"); err == nil || !strings.Contains(err.Error(), "記事が見つかりません") {
		t.Fatalf("err = %v", err)
	}
}

func TestFuwaleteURL(t *testing.T) {
	for _, tt := range []struct {
		in   string
		ok   bool
		slug string
	}{
		{"https://fuwalete.com/ito-365549/", true, "ito-365549"},
		{"https://www.fuwalete.com/toyo-367599", true, "toyo-367599"},
		{"https://fuwalete.com/", true, ""},
		{"https://fuwalete.com/category/x/", true, ""},
		{"https://shogidb2.com/games/abc", false, ""},
	} {
		if got := IsFuwaleteURL(tt.in); got != tt.ok {
			t.Errorf("IsFuwaleteURL(%q) = %v", tt.in, got)
		}
		if !tt.ok {
			continue
		}
		slug, err := FuwaleteSlug(tt.in)
		if tt.slug == "" {
			if err == nil {
				t.Errorf("FuwaleteSlug(%q) = %q, want error", tt.in, slug)
			}
			continue
		}
		if err != nil || slug != tt.slug {
			t.Errorf("FuwaleteSlug(%q) = %q, %v", tt.in, slug, err)
		}
	}
}
