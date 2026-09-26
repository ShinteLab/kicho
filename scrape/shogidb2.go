package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/websocket"
	"golang.org/x/text/width"

	"github.com/ShinteLab/core/kifu"
)

// ShogiDB2 は将棋DB2(shogidb2.com)の棋譜を扱う。
//
// **スクレイピングだが、HTML は読まない。** ページは Phoenix LiveView で、
// 棋譜はサーバの HTML に入っていない。「KIF形式」ボタン(`phx-click="kif"`)を
// 押すとサーバが `push_event("kif", {data})` で**構造化データ**を返し、
// ブラウザ側(KifExporter)がそれを KIF の文字にしている。
// こちらは同じ口を WebSocket で叩いて data を受け取り、読売と同じく
// **構造化データから KIF を組み立てる**。
//
// サイトが作る KIF(textarea に出るもの)は使わない。1 手ごとの消費時間を
// 持っていないのに `( 0:00/00:00:00)` で埋めた作り物なので、原本扱いできない。
//
// 壊れやすい点(サイトの作りに依存している):
//
//   - LiveView のプロトコル版 `vsn=2.0.0`(メッセージが [join_ref, ref, topic, event, payload] の配列)
//   - ページの `<meta name="csrf-token">` と LiveView のルート要素
//     (`id="phx-..."`・`data-phx-session`・`data-phx-static`)。join にそのまま渡す
//   - セッション Cookie(ページを取ったときの Set-Cookie)を WebSocket にも付けること。
//     無いと join が `unauthorized` / `stale` で弾かれる
//   - イベント名 `kif` と、返りの `response.diff.e` に入る `["kif", {data}]` の形
type ShogiDB2 struct {
	Client *http.Client
	// BaseURL は取得元(テストで差し替える)。既定は ShogiDB2BaseURL。
	// WebSocket の URL もここから作る(http → ws / https → wss)。
	BaseURL string
}

// ShogiDB2BaseURL は将棋DB2 の基点。
const ShogiDB2BaseURL = "https://shogidb2.com"

// shogiDB2Vsn は LiveView のプロトコル版。サイトの phoenix_live_view が
// 上がってメッセージの形が変わったらここが合わなくなる。
const shogiDB2Vsn = "2.0.0"

// NewShogiDB2 は既定の HTTP クライアントを持つ ShogiDB2 を返す。
func NewShogiDB2() *ShogiDB2 {
	return &ShogiDB2{Client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *ShogiDB2) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *ShogiDB2) base() string {
	if s.BaseURL != "" {
		return strings.TrimSuffix(s.BaseURL, "/")
	}
	return ShogiDB2BaseURL
}

// GameURL は棋譜 ID から対局ページの URL を組み立てる。
// 保存した棋譜の諸元(どこから取ったか)として残す。
func (s *ShogiDB2) GameURL(id string) string {
	return s.base() + "/games/" + url.PathEscape(id)
}

// IsShogiDB2URL は shogidb2.com の URL かどうかを返す。
// 取得タブの入力をどの取得元に回すかの判定に使う。
func IsShogiDB2URL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "shogidb2.com" || host == "www.shogidb2.com"
}

// ShogiDB2GameID は対局ページの URL(`https://shogidb2.com/games/{id}`)から
// 棋譜 ID(ハッシュ)を取り出す。
func ShogiDB2GameID(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("URL の形式が不正です: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "games" || parts[1] == "" {
		return "", fmt.Errorf("将棋DB2 の対局ページの URL ではありません(/games/{ID} の形を貼ってください): %s", raw)
	}
	return parts[1], nil
}

// ShogiDB2Game は将棋DB2 から取得した1局分。
type ShogiDB2Game struct {
	SourceID  string // 棋譜 ID(URL の /games/{id})
	Event     string // 棋戦名(tournament_detail。無ければ tournament)
	Handicap  string // 手合割
	Place     string // 対局場所
	Black     string // 先手(player1)
	White     string // 後手(player2)
	StartedAt time.Time
	// EndMark は終局の種別(例 "投了")。対局中は空。
	EndMark   string
	TimeLimit time.Duration
	// Moves は終局の手(EndMark)を含む。
	Moves []kifu.Move
}

// Finished は終局しているかどうかを返す(対局中なら false)。
func (g ShogiDB2Game) Finished() bool { return g.EndMark != "" }

// Document は core/kifu の Document に変換する。
//
// 1 手ごとの消費時間は取れないので **ShowTime は立てない**
// (立てると 0:00 が並び、サイトの KIF と同じ作り物になる)。
func (g ShogiDB2Game) Document() kifu.Document {
	return kifu.Document{
		Handicap:  g.Handicap,
		Event:     g.Event,
		Place:     g.Place,
		StartedAt: g.StartedAt,
		Black:     g.Black,
		White:     g.White,
		TimeLimit: g.TimeLimit,
		Moves:     g.Moves,
	}
}

// KIF は KIF テキストに変換する。
func (g ShogiDB2Game) KIF() string { return g.Document().String() }

// FetchGame は棋譜 ID から1局分を取得する。
//
// ページを取って LiveView に join し、「KIF形式」ボタンと同じイベントを送って
// 返ってきた構造化データから組み立てる。**ctx の期限で必ず抜ける**
// (期限が無ければ 30 秒で切る)。
func (s *ShogiDB2) FetchGame(ctx context.Context, id string) (*ShogiDB2Game, error) {
	id = strings.Trim(strings.TrimSpace(id), "/")
	if id == "" {
		return nil, fmt.Errorf("scrape: empty shogidb2 game id")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	pageURL := s.GameURL(id)
	lv, cookies, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	data, err := s.pushKif(ctx, pageURL, lv, cookies)
	if err != nil {
		return nil, err
	}
	g, err := data.game()
	if err != nil {
		return nil, fmt.Errorf("将棋DB2 %s: %w", id, err)
	}
	g.SourceID = id
	return g, nil
}

// FetchKifu は棋譜 ID から KIF 本文だけを返す(HTTP のライブ経路用)。
func (s *ShogiDB2) FetchKifu(ctx context.Context, id string) (string, error) {
	g, err := s.FetchGame(ctx, id)
	if err != nil {
		return "", err
	}
	return g.KIF(), nil
}

// liveView は join に要るページ側の値。
type liveView struct {
	csrf    string
	id      string // ルート要素の id("phx-...")。topic は "lv:" + id
	session string
	static  string
}

// fetchPage は対局ページを取り、LiveView の値と Cookie を返す。
func (s *ShogiDB2) fetchPage(ctx context.Context, pageURL string) (liveView, []*http.Cookie, error) {
	jar, _ := cookiejar.New(nil)
	c := *s.client() // Cookie はこの取得の間だけ持つ(クライアントは共有なので Jar を足さない)
	c.Jar = jar

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return liveView{}, nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return liveView{}, nil, fmt.Errorf("将棋DB2 のページを取得できませんでした: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return liveView{}, nil, fmt.Errorf("将棋DB2 に棋譜が見つかりません(HTTP 404): %s", pageURL)
	}
	if resp.StatusCode != http.StatusOK {
		return liveView{}, nil, fmt.Errorf("将棋DB2 のページを取得できませんでした(HTTP %d): %s", resp.StatusCode, pageURL)
	}
	b, err := ReadLimited(resp.Body)
	if err != nil {
		return liveView{}, nil, err
	}
	lv, err := parseLiveView(b)
	if err != nil {
		return liveView{}, nil, err
	}
	return lv, jar.Cookies(resp.Request.URL), nil
}

// parseLiveView はページから csrf と LiveView のルート要素の値を拾う。
func parseLiveView(page []byte) (liveView, error) {
	var lv liveView
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch z.Next() {
		case html.ErrorToken:
			switch {
			case lv.csrf == "":
				return lv, errors.New("将棋DB2 のページに csrf-token がありません(サイトの作りが変わった可能性があります)")
			case lv.session == "":
				return lv, errors.New("将棋DB2 のページに LiveView が見つかりません(サイトの作りが変わった可能性があります)")
			}
			return lv, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			attr := func(key string) (string, bool) {
				for _, a := range t.Attr {
					if a.Key == key {
						return a.Val, true
					}
				}
				return "", false
			}
			if t.Data == "meta" {
				if name, _ := attr("name"); name == "csrf-token" && lv.csrf == "" {
					lv.csrf, _ = attr("content")
				}
				continue
			}
			// ルート要素は data-phx-session を持つ最初の要素(入れ子の LiveView は後に出る)。
			if session, ok := attr("data-phx-session"); ok && lv.session == "" {
				lv.session = session
				lv.id, _ = attr("id")
				lv.static, _ = attr("data-phx-static")
			}
		}
	}
}

// wsURL は LiveView の WebSocket の URL を作る。
func (s *ShogiDB2) wsURL(csrf string) (string, string, error) {
	u, err := url.Parse(s.base())
	if err != nil {
		return "", "", err
	}
	origin := u.Scheme + "://" + u.Host
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", "", fmt.Errorf("scrape: shogidb2 の BaseURL が http(s) ではありません: %s", s.base())
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/live/websocket"
	u.RawQuery = url.Values{"_csrf_token": {csrf}, "vsn": {shogiDB2Vsn}, "locale": {"ja"}}.Encode()
	return u.String(), origin, nil
}

// pushKif は LiveView に join し、「KIF形式」ボタンのイベントを送って data を受け取る。
func (s *ShogiDB2) pushKif(ctx context.Context, pageURL string, lv liveView, cookies []*http.Cookie) (*shogiDB2Data, error) {
	wsURL, origin, err := s.wsURL(lv.csrf)
	if err != nil {
		return nil, err
	}
	cfg, err := websocket.NewConfig(wsURL, origin)
	if err != nil {
		return nil, err
	}
	cfg.Header = http.Header{}
	for _, c := range cookies {
		cfg.Header.Add("Cookie", c.Name+"="+c.Value)
	}
	conn, err := cfg.DialContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("将棋DB2 に接続できませんでした: %w", err)
	}
	defer conn.Close()
	// 読み書きは ctx を見ないので、期限を移し、取り消しでは接続ごと閉じて抜ける。
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	topic := "lv:" + lv.id
	send := func(ref, event string, payload any) error {
		b, err := json.Marshal([]any{"1", ref, topic, event, payload})
		if err != nil {
			return err
		}
		if err := websocket.Message.Send(conn, string(b)); err != nil {
			return fmt.Errorf("将棋DB2 への送信に失敗しました: %w", ctxErr(ctx, err))
		}
		return nil
	}

	if err := send("1", "phx_join", map[string]any{
		"url":     pageURL,
		"params":  map[string]any{"_csrf_token": lv.csrf, "_mounts": 0, "locale": "ja"},
		"session": lv.session,
		"static":  lv.static,
	}); err != nil {
		return nil, err
	}
	if _, err := readReply(ctx, conn, topic, "1"); err != nil {
		return nil, fmt.Errorf("将棋DB2 の LiveView に参加できませんでした: %w", err)
	}

	if err := send("2", "event", map[string]any{"type": "click", "event": "kif", "value": map[string]any{}}); err != nil {
		return nil, err
	}
	resp, err := readReply(ctx, conn, topic, "2")
	if err != nil {
		return nil, fmt.Errorf("将棋DB2 から棋譜を受け取れませんでした: %w", err)
	}
	return kifFromReply(resp)
}

// readReply は ref の phx_reply が来るまで読み、ok なら response を返す。
// 途中に来る他のメッセージ(diff の push など)は読み捨てる。
func readReply(ctx context.Context, conn *websocket.Conn, topic, ref string) (json.RawMessage, error) {
	for {
		var msg string
		if err := websocket.Message.Receive(conn, &msg); err != nil {
			return nil, ctxErr(ctx, err)
		}
		var frame []json.RawMessage
		if err := json.Unmarshal([]byte(msg), &frame); err != nil || len(frame) != 5 {
			return nil, fmt.Errorf("LiveView の応答が読めません(プロトコルが変わった可能性があります): %.200s", msg)
		}
		var gotTopic, gotRef, event string
		_ = json.Unmarshal(frame[1], &gotRef)
		_ = json.Unmarshal(frame[2], &gotTopic)
		_ = json.Unmarshal(frame[3], &event)
		if gotTopic != topic {
			continue
		}
		switch event {
		case "phx_error", "phx_close":
			return nil, fmt.Errorf("サーバが接続を閉じました(%s)", event)
		case "phx_reply":
			if gotRef != ref {
				continue
			}
			var reply struct {
				Status   string          `json:"status"`
				Response json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal(frame[4], &reply); err != nil {
				return nil, fmt.Errorf("LiveView の応答が読めません: %w", err)
			}
			if reply.Status != "ok" {
				return nil, fmt.Errorf("サーバがエラーを返しました(%s): %.200s", reply.Status, reply.Response)
			}
			return reply.Response, nil
		}
	}
}

// ctxErr は ctx が終わっていればそちらを理由にする(接続を閉じた結果の読み取りエラーより分かりやすい)。
func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("時間切れです: %w", ctx.Err())
		}
		return ctx.Err()
	}
	return err
}

// kifFromReply はイベントの返りの `diff.e` から `["kif", {data}]` を探す。
func kifFromReply(resp json.RawMessage) (*shogiDB2Data, error) {
	var r struct {
		Diff struct {
			Events [][]json.RawMessage `json:"e"`
		} `json:"diff"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return nil, fmt.Errorf("応答が読めません: %w", err)
	}
	for _, e := range r.Diff.Events {
		if len(e) != 2 {
			continue
		}
		var name string
		if json.Unmarshal(e[0], &name) != nil || name != "kif" {
			continue
		}
		var payload struct {
			Data *shogiDB2Data `json:"data"`
		}
		if err := json.Unmarshal(e[1], &payload); err != nil {
			return nil, fmt.Errorf("棋譜のデータが読めません: %w", err)
		}
		if payload.Data == nil {
			break
		}
		return payload.Data, nil
	}
	return nil, errors.New("応答に棋譜(kif イベント)がありません(サイトの作りが変わった可能性があります)")
}

// shogiDB2Data は push_event("kif") の data。使うものだけ。
type shogiDB2Data struct {
	Player1          string `json:"player1"` // 先手(サイトの KifExporter も player1 を先手に書く)
	Player2          string `json:"player2"` // 後手
	Tournament       string `json:"tournament"`
	TournamentDetail string `json:"tournament_detail"`
	Place            string `json:"place"`
	Time             string `json:"time"` // 持ち時間。"４時間" のように全角数字
	Handicap         string `json:"handicap"`
	StartAt          string `json:"start_at"` // UTC(表示は JST)
	Moves            []struct {
		Num int    `json:"num"`
		CSA string `json:"csa"` // "+2726FU"。終局は "%TORYO"
	} `json:"moves"`
}

// game は data を棋譜にする。
//
// 指し手は CSA で来るので、開始局面から USI に直し(DecodeCSA)、
// そこから日本語表記と移動元を作る(FormatMoves)。
// **サイトの label("▲26歩(27)")は使わない** —— 算用数字で KIF の表記ではない。
func (d *shogiDB2Data) game() (*ShogiDB2Game, error) {
	handicap := strings.TrimSpace(d.Handicap)
	if handicap == "" {
		handicap = kifu.HirateHandicap
	}
	start, err := kifu.StartSFEN(handicap)
	if err != nil {
		return nil, err
	}

	moves := d.Moves
	sort.SliceStable(moves, func(i, j int) bool { return moves[i].Num < moves[j].Num })
	csa := make([]string, 0, len(moves))
	for _, m := range moves {
		csa = append(csa, m.CSA)
	}
	usiMoves, end, err := kifu.DecodeCSA(start, csa)
	if err != nil {
		return nil, fmt.Errorf("指し手が読めません: %w", err)
	}
	texts, err := kifu.FormatMoves(start, usiMoves)
	if err != nil {
		return nil, fmt.Errorf("指し手を表記にできません: %w", err)
	}

	out := make([]kifu.Move, 0, len(texts)+1)
	for i, t := range texts {
		out = append(out, kifu.Move{Num: i + 1, Name: t.Name, FromX: t.FromX, FromY: t.FromY})
	}
	if end != "" {
		out = append(out, kifu.Move{Num: len(texts) + 1, Name: end})
	}

	g := &ShogiDB2Game{
		Event:     strings.TrimSpace(d.TournamentDetail),
		Handicap:  handicap,
		Place:     strings.TrimSpace(d.Place),
		Black:     strings.TrimSpace(d.Player1),
		White:     strings.TrimSpace(d.Player2),
		EndMark:   end,
		TimeLimit: parseShogiDB2Time(d.Time),
		Moves:     out,
	}
	if g.Event == "" {
		g.Event = strings.TrimSpace(d.Tournament)
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(d.StartAt)); err == nil {
		g.StartedAt = t.In(jst)
	}
	return g, nil
}

var (
	shogiDB2Hours   = regexp.MustCompile(`(\d+)時間`)
	shogiDB2Minutes = regexp.MustCompile(`(\d+)分`)
)

// parseShogiDB2Time は持ち時間("４時間" / "各5時間" / "1時間30分")を読む。
// 読めなければ 0(ヘッダに出さない)。全角数字は半角に寄せてから読む。
func parseShogiDB2Time(s string) time.Duration {
	s = width.Fold.String(s)
	var d time.Duration
	if m := shogiDB2Hours.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		d += time.Duration(n) * time.Hour
	}
	if m := shogiDB2Minutes.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		d += time.Duration(n) * time.Minute
	}
	return d
}
