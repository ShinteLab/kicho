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

	"github.com/ShinteLab/core/kifu"

	"github.com/ShinteLab/kicho/format"
)

// testdata は実サイトの REST API の応答そのまま(2026-11-18 に取得)。
//
//	fuwalete_post_finished.json … 伊藤–永瀬戦(王将戦 挑決L。投了まで。74 手目に誤記がある)
//	fuwalete_post_live.json     … 豊島–広瀬戦(順位戦 A級。対局中。記事に 37 手)
//	fuwalete_live_page.json     … 対局中の記事が読み込む枠(/q22b/)。見出しだけで手はまだ無い
//	fuwalete_post_rematch.json  … 豊島–広瀬戦の終局後(2026-11-19 に取得)。87 手で千日手、
//	                              指し直し局が 114 手で投了。1 つの記事に 2 局が並ぶ。
//	                              指し直し局の 87 手目が「▲65角26歩」(付記の前の空白が抜けている)

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

// toKIF は原本(KI2)を KIF にする(ライブ経路・/kifu/{id}.kif と同じ変換)。
func toKIF(t *testing.T, g *FuwaleteGame) string {
	t.Helper()
	kif, err := format.KI2ToKIF(g.KI2())
	if err != nil {
		t.Fatal(err)
	}
	return kif
}

// 誤記があっても原本(KI2)は全部の手を持つ。推測で直さず、切りもしない。
// KIF にすると誤記の手の前で止まり、どこで止まったかがコメントに入る。
func TestFuwaleteKeepsEveryMoveDespiteTypo(t *testing.T) {
	s := fuwaleteServer(t, map[string]string{"ito-365549": readTestdata(t, "fuwalete_post_finished.json")}, nil)
	g, err := s.FetchGame(context.Background(), "ito-365549")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Moves) != 97 || g.EndMark != "投了" {
		t.Fatalf("手数 = %d, EndMark = %q, want 97 / 投了(サイトに書かれたまま)", len(g.Moves), g.EndMark)
	}
	if got := g.Moves[73].Text; got != "△８九飛" {
		t.Errorf("74 手目 = %q, want 誤記のまま △８九飛", got)
	}

	ki2 := g.KI2()
	for _, line := range []string{"▲５五金", "△８九飛", "▲同　玉", "まで97手で先手の勝ち"} {
		if !strings.Contains(ki2, line) {
			t.Errorf("KI2 に %q が無い", line)
		}
	}
	// 原本は読み直しても同じ。
	if k, err := kifu.ParseKI2(ki2); err != nil || len(k.Moves) != 97 || k.End != "投了" {
		t.Fatalf("ParseKI2: 手数 = %d, End = %q, err = %v", len(k.Moves), k.End, err)
	}

	kif := toKIF(t, g)
	if !strings.Contains(kif, "*74 手目「△８九飛」を盤に当てられないため、73 手目までです") {
		t.Errorf("KIF の先頭に止まった理由が無い:\n%s", kif[:400])
	}
	if !strings.Contains(kif, "73 同　玉(58)") || strings.Contains(kif, "\n74 ") || strings.Contains(kif, "投了") {
		t.Errorf("KIF が 73 手目で止まっていない")
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
	if !g.Finished() || g.EndMark != "投了" || len(g.Moves) != 97 {
		t.Fatalf("EndMark = %q, 手数 = %d", g.EndMark, len(g.Moves))
	}

	kif := toKIF(t, g)
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
	if strings.Contains(kif, "盤に当てられない") {
		t.Error("全部読めたのに止まった理由が入っている")
	}
	// 「ーー 午後 開始 ーー」のような区切りは解説にしない。
	if strings.Contains(kif, "ーー") || strings.Contains(g.KI2(), "ーー") {
		t.Error("区切りの行が解説に入っている")
	}
}

// 手数が欠けていたら置き場("△？")を入れ、そこから先の手も残す。
func TestFuwaleteGapPlaceholder(t *testing.T) {
	body := editContent(t, readTestdata(t, "fuwalete_post_finished.json"), func(c string) string {
		c = strings.Replace(c, "74手　86%　△89飛", "74手　86%　△29飛", 1)
		return strings.Replace(c, "50手　51%　△同歩<br>", "", 1)
	})
	s := fuwaleteServer(t, map[string]string{"ito-365549": body}, nil)
	g, err := s.FetchGame(context.Background(), "ito-365549")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Moves) != 97 {
		t.Fatalf("手数 = %d, want 97", len(g.Moves))
	}
	if m := g.Moves[49]; m.Text != "△？" || !strings.Contains(m.Comment, "50 手目がありません") {
		t.Errorf("50 手目 = %+v", m)
	}
	if kif := toKIF(t, g); !strings.Contains(kif, "*50 手目「△？」を盤に当てられないため、49 手目までです") {
		t.Errorf("KIF に止まった理由が無い:\n%s", kif[:400])
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
		if len(g.Moves) != 37 || g.Finished() {
			t.Fatalf("手数 = %d, EndMark = %q", len(g.Moves), g.EndMark)
		}
		if g.Black != "豊島将之" || g.White != "広瀬章人" || g.Event != "順位戦 A級" {
			t.Errorf("先手 = %q, 後手 = %q, 棋戦 = %q", g.Black, g.White, g.Event)
		}
		if g.TimeLimit != 6*time.Hour {
			t.Errorf("TimeLimit = %v", g.TimeLimit)
		}
		// ライブ経路は KIF で配る。
		kif, err := s.FetchKifu(context.Background(), "toyo-367599")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(kif, "37 ３六歩(37)") || strings.Contains(kif, "盤に当てられない") {
			t.Errorf("ライブ経路の KIF:\n%s", kif)
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
		if len(g.Moves) != 38 || g.Moves[37].Text != "△４四歩" {
			t.Fatalf("手数 = %d, 最後 = %+v", len(g.Moves), g.Moves[len(g.Moves)-1])
		}
		if kif := toKIF(t, g); !strings.Contains(kif, "38 ４四歩(43)") {
			t.Errorf("KIF に 38 手目が無い")
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

// 指し直しのある記事は、区切りより上の指し直し局だけを読む(先後も指し直し局のもの)。
func TestFuwaleteRematch(t *testing.T) {
	s := fuwaleteServer(t, map[string]string{"toyo-367599": readTestdata(t, "fuwalete_post_rematch.json")}, nil)
	g, err := s.FetchGame(context.Background(), "toyo-367599")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Moves) != 114 || g.EndMark != "投了" {
		t.Fatalf("手数 = %d, EndMark = %q, want 114 / 投了", len(g.Moves), g.EndMark)
	}
	// 千日手局は先手が豊島、指し直し局は先手が広瀬。
	if g.Black != "広瀬章人" || g.White != "豊島将之" {
		t.Errorf("先手 = %q, 後手 = %q", g.Black, g.White)
	}
	if g.TimeLimit != 6*time.Hour {
		t.Errorf("TimeLimit = %v", g.TimeLimit)
	}
	// 指し直し局であることは棋戦名に付ける(KI2 の「棋戦：」にも出る)。
	if g.Event != "順位戦 A級（指し直し）" {
		t.Errorf("Event = %q", g.Event)
	}
	if !strings.Contains(g.KI2(), "棋戦：順位戦 A級（指し直し）") {
		t.Errorf("KI2 の棋戦に指し直しが無い:\n%s", g.KI2()[:200])
	}
	// 「▲65角26歩」の 26歩 は空白の抜けた付記。
	if got := g.Moves[86].Text; got != "▲６五角" {
		t.Errorf("87 手目 = %q, want ▲６五角", got)
	}
	kif := toKIF(t, g)
	if strings.Contains(kif, "盤に当てられない") || !strings.Contains(kif, "115 投了") {
		t.Errorf("投了まで読めていない:\n%s", kif)
	}
}

func TestFuwaleteMoveText(t *testing.T) {
	for in, want := range map[string]string{
		"▲55金 0:37":       "▲55金",
		"▲66角 ai36角 0:24": "▲66角",
		"▲同玉 9/1:10":      "▲同玉",
		"▲同 玉":            "▲同玉",
		"▲53銀引成":          "▲53銀引成",
		"▲65角26歩 0:31":    "▲65角",
		"▲65角ai26歩":       "▲65角",
		"▲同歩0:12":         "▲同歩",
		"△37歩成2":          "△37歩成",
	} {
		if got := fuwaleteMoveText(in); got != want {
			t.Errorf("fuwaleteMoveText(%q) = %q, want %q", in, got, want)
		}
	}
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
