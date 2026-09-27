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

	"golang.org/x/net/websocket"
)

// shogiDB2ID は実サイトの対局(ALSOK杯第76期王将戦 挑戦者決定リーグ戦 伊藤匠二冠 vs 郷田真隆九段)。
// testdata/shogidb2_kif_reply.json はこの対局で「KIF形式」を押したときの実際の応答
// (2026-09-26 取得。LiveView の topic もそのときのもの)。
const shogiDB2ID = "3186d1e7f8f33242990b1e853f53250d443dbe25132936b005068009a201"

const shogiDB2Topic = "lv:phx-GNi8g9H9uNSE27Yx"

// shogiDB2Page は対局ページの HTML(join に要るところだけ)。
const shogiDB2Page = `<!DOCTYPE html>
<html lang="ja"><head>
<meta charset="utf-8">
<meta name="csrf-token" content="CSRF-TOKEN">
<title>将棋DB2</title>
</head><body>
<div id="phx-GNi8g9H9uNSE27Yx" data-phx-main data-phx-session="SESSION-TOKEN" data-phx-static="STATIC-TOKEN">
<div id="kifu-modal"><textarea></textarea></div>
</div>
</body></html>`

// db2Server は将棋DB2 の代役。
type db2Server struct {
	srv *httptest.Server
	// reply はイベント(kif)の返りに送るメッセージ。
	reply string
	// joinStatus が "ok" 以外なら join を弾く。
	joinStatus string
	// hang が true なら join に返事をしない(タイムアウトの確認用)。
	hang bool
	// join は受け取った join の payload。
	join map[string]any
	// header は WebSocket のハンドシェイクで受け取ったもの。
	header http.Header
	query  map[string]string
}

func newShogiDB2Server(t *testing.T) (*ShogiDB2, *db2Server) {
	t.Helper()
	reply, err := os.ReadFile("testdata/shogidb2_kif_reply.json")
	if err != nil {
		t.Fatal(err)
	}
	d := &db2Server{reply: string(reply), joinStatus: "ok"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /games/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != shogiDB2ID {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "_shogidb_key", Value: "COOKIE", Path: "/"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(shogiDB2Page))
	})
	mux.Handle("/live/websocket", websocket.Handler(func(ws *websocket.Conn) {
		r := ws.Request()
		d.header = r.Header.Clone()
		d.query = map[string]string{}
		for k := range r.URL.Query() {
			d.query[k] = r.URL.Query().Get(k)
		}
		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}
			var frame []any
			if err := json.Unmarshal([]byte(msg), &frame); err != nil || len(frame) != 5 {
				t.Errorf("送られてきた形が違う: %s", msg)
				return
			}
			ref, _ := frame[1].(string)
			switch frame[3] {
			case "phx_join":
				d.join, _ = frame[4].(map[string]any)
				if d.hang {
					continue
				}
				// 関係ないメッセージが先に来ても読み飛ばすこと。
				websocket.Message.Send(ws, `[null,null,"phoenix","phx_reply",{"status":"ok","response":{}}]`)
				websocket.Message.Send(ws, `["1","1","`+shogiDB2Topic+`","phx_reply",{"status":"`+d.joinStatus+`","response":{"reason":"stale"}}]`)
			case "event":
				p, _ := frame[4].(map[string]any)
				if p["event"] != "kif" || p["type"] != "click" {
					t.Errorf("イベントが違う: %v", p)
				}
				if ref != "2" {
					t.Errorf("ref = %q", ref)
				}
				websocket.Message.Send(ws, `["1",null,"`+shogiDB2Topic+`","diff",{"0":"x"}]`)
				websocket.Message.Send(ws, d.reply)
			}
		}
	}))
	d.srv = httptest.NewServer(mux)
	t.Cleanup(d.srv.Close)

	return &ShogiDB2{Client: &http.Client{Timeout: 5 * time.Second}, BaseURL: d.srv.URL}, d
}

// join → event → reply の往復で、実際の応答から 145 手＋投了の棋譜が組み上がること。
func TestShogiDB2FetchGame(t *testing.T) {
	s, d := newShogiDB2Server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	g, err := s.FetchGame(ctx, shogiDB2ID)
	if err != nil {
		t.Fatal(err)
	}

	// join にページの値と Cookie を渡していること(無いと実サイトは弾く)。
	if d.query["_csrf_token"] != "CSRF-TOKEN" || d.query["vsn"] != "2.0.0" {
		t.Errorf("query = %v", d.query)
	}
	if !strings.Contains(d.header.Get("Cookie"), "_shogidb_key=COOKIE") {
		t.Errorf("Cookie = %q", d.header.Get("Cookie"))
	}
	if d.header.Get("Origin") != d.srv.URL {
		t.Errorf("Origin = %q, want %q", d.header.Get("Origin"), d.srv.URL)
	}
	if d.join["session"] != "SESSION-TOKEN" || d.join["static"] != "STATIC-TOKEN" ||
		d.join["url"] != d.srv.URL+"/games/"+shogiDB2ID {
		t.Errorf("join = %v", d.join)
	}

	if g.SourceID != shogiDB2ID {
		t.Errorf("SourceID = %q", g.SourceID)
	}
	// player1 が先手(サイトの KifExporter と同じ)。
	if g.Black != "伊藤　匠 二冠" || g.White != "郷田真隆 九段" {
		t.Errorf("対局者 = %q / %q", g.Black, g.White)
	}
	if g.Event != "ALSOK杯第76期王将戦 挑戦者決定リーグ戦" || g.Place != "東京・将棋会館" || g.Handicap != "平手" {
		t.Errorf("棋戦 = %q, 場所 = %q, 手合割 = %q", g.Event, g.Place, g.Handicap)
	}
	// start_at は UTC。JST に直して持つ。
	if got := g.StartedAt.Format("2006-01-02 15:04 MST"); got != "2026-09-25 10:00 JST" {
		t.Errorf("StartedAt = %s", got)
	}
	if g.TimeLimit != 4*time.Hour {
		t.Errorf("TimeLimit = %v", g.TimeLimit)
	}
	if !g.Finished() || g.EndMark != "投了" {
		t.Errorf("EndMark = %q", g.EndMark)
	}
	if len(g.Moves) != 146 {
		t.Fatalf("手数 = %d, want 145 手＋投了", len(g.Moves))
	}

	kif := g.KIF()
	for _, want := range []string{
		"開始日時：2026/09/25 10:00:00",
		"棋戦：ALSOK杯第76期王将戦 挑戦者決定リーグ戦",
		"持ち時間：各4時間",
		"先手：伊藤　匠 二冠",
		"\n1 ２六歩(27)\n",
		"\n10 ７七角成(22)\n", // CSA は "-2277UM"(成った手)
		"\n11 同　銀(88)\n",
		"\n145 ４四桂打(00)\n",
		"\n146 投了\n",
	} {
		if !strings.Contains(kif, want) {
			t.Errorf("KIF に %q がありません:\n%s", want, kif)
		}
	}
	// 消費時間は取れないので欄ごと出さない(0:00 で埋めない)。
	if strings.Contains(kif, "0:00/") {
		t.Errorf("消費時間欄が出ています:\n%s", kif)
	}
}

func TestShogiDB2NotFound(t *testing.T) {
	s, _ := newShogiDB2Server(t)
	_, err := s.FetchGame(context.Background(), "nothing")
	if err == nil || !strings.Contains(err.Error(), "見つかりません") {
		t.Fatalf("err = %v", err)
	}
}

func TestShogiDB2JoinRejected(t *testing.T) {
	s, d := newShogiDB2Server(t)
	d.joinStatus = "error"
	_, err := s.FetchGame(context.Background(), shogiDB2ID)
	if err == nil || !strings.Contains(err.Error(), "参加できませんでした") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("err = %v", err)
	}
}

func TestShogiDB2NoKifEvent(t *testing.T) {
	s, d := newShogiDB2Server(t)
	d.reply = `["1","2","` + shogiDB2Topic + `","phx_reply",{"status":"ok","response":{"diff":{}}}]`
	_, err := s.FetchGame(context.Background(), shogiDB2ID)
	if err == nil || !strings.Contains(err.Error(), "kif イベント") {
		t.Fatalf("err = %v", err)
	}
}

// サーバが黙っても ctx の期限で抜けること。
func TestShogiDB2Timeout(t *testing.T) {
	s, d := newShogiDB2Server(t)
	d.hang = true
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := s.FetchGame(ctx, shogiDB2ID)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "時間切れ") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx の期限を過ぎても戻らない")
	}
}

func TestShogiDB2URL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://shogidb2.com/games/abc":     true,
		"https://www.shogidb2.com/games/abc": true,
		"https://example.com/games/abc":      false,
		"abc":                                false,
	} {
		if got := IsShogiDB2URL(raw); got != want {
			t.Errorf("IsShogiDB2URL(%q) = %v", raw, got)
		}
	}

	id, err := ShogiDB2GameID("https://shogidb2.com/games/" + shogiDB2ID + "?tab=1#x")
	if err != nil || id != shogiDB2ID {
		t.Errorf("ShogiDB2GameID = %q, %v", id, err)
	}
	if _, err := ShogiDB2GameID("https://shogidb2.com/tournament/王将戦"); err == nil {
		t.Error("対局ページ以外を受け付けた")
	}
}

func TestParseShogiDB2Time(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"４時間":    4 * time.Hour,
		"各5時間":   5 * time.Hour,
		"１時間３０分": 90 * time.Minute,
		"２０分":    20 * time.Minute,
		"":       0,
		"不明":     0,
	} {
		if got := parseShogiDB2Time(in); got != want {
			t.Errorf("parseShogiDB2Time(%q) = %v, want %v", in, got, want)
		}
	}
}
