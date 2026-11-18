package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/ShinteLab/core/kifu"

	"github.com/ShinteLab/kicho/format"
)

// Fuwalete は「ふわりと将棋」(fuwalete.com)の棋譜速報を扱う。
//
// **人が手で書いているブログ記事を読む。** サイトは KIF も構造化データも配っておらず、
// 指し手は記事の本文に**新しい手が上**の順で、**移動元を書かない KI2 の表記**で並ぶ:
//
//	97手　92%　▲55金 0:37
//	87手　89%　▲53銀引成 0:46
//
// **原本はその指し手をそのまま写した KI2**(`FuwaleteGame.KI2()`)。KIF が要るとき
// (HTTP のライブ経路・`/kifu/{id}.kif`)は、盤を開始局面から進めながらどの駒が来たのかを
// 割り出して(`kifu.DecodeKI2`)変換する。
//
// ⚠️ **手入力なので誤記がある**(実例: 伊藤–永瀬戦の 74 手目「△89飛」は △29飛打 の誤り。
// `_docs/sources/fuwalete.md`)。**推測で直さないこと。原本の手も切らないこと。**
// 盤に当てられない手は**エラーの印**として扱う(どの手で止まるかは使う側が本文から計算する)。
// 訂正は kicho の外の仕事で、訂正した棋譜は親子(派生棋譜)として別に残る。
//
// HTML のページ(700KB 超)ではなく WordPress の REST API(`/wp-json/wp/v2/posts?slug=`)で
// 本文だけを取る(20KB 程度)。
//
// 壊れやすい点(サイトの作りに依存している):
//
//   - 指し手の行の形 `{手数}手　{評価値}%　{▲△}{KI2}`(`fuwaleteMoveLine`)
//   - 対局中は**最新の指し手が別のページ(共有の枠。例 /q22b/)にあり**、記事は
//     `var url = "https://fuwalete.com/q22b/"` の JS でそれを読み込んでいる(`fuwaleteLiveURL`)。
//     終局すると記事に全部が移り、この JS は消える
//   - 先後は見出しの `後手△伊藤叡王　先手▲永瀬九段`。対局者の名前と棋戦名は記事の題
//     `11.6(金) 王将戦 挑決L 伊藤匠 永瀬拓矢 10時～棋譜速報`
type Fuwalete struct {
	Client *http.Client
	// BaseURL は取得元(テストで差し替える)。既定は FuwaleteBaseURL。
	BaseURL string
	// MinInterval は同じ棋譜を取りに行く最短の間隔(内側なら前の結果を返す。`MinFetchInterval`)。
	// 0 なら毎回取りに行く。`NewXxx` は MinFetchInterval を入れる(構造体を直接作ると 0)。
	MinInterval time.Duration

	recent recent[*FuwaleteGame]
}

// FuwaleteBaseURL は「ふわりと将棋」の基点。
const FuwaleteBaseURL = "https://fuwalete.com"

// NewFuwalete は既定の HTTP クライアントを持つ Fuwalete を返す。
func NewFuwalete() *Fuwalete {
	return &Fuwalete{Client: &http.Client{Timeout: 30 * time.Second}, MinInterval: MinFetchInterval}
}

func (s *Fuwalete) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *Fuwalete) base() string {
	if s.BaseURL != "" {
		return strings.TrimSuffix(s.BaseURL, "/")
	}
	return FuwaleteBaseURL
}

// PostURL は記事の slug から記事の URL を組み立てる(保存した棋譜の諸元に残す)。
func (s *Fuwalete) PostURL(slug string) string {
	return s.base() + "/" + url.PathEscape(slug) + "/"
}

// IsFuwaleteURL は fuwalete.com の URL かどうかを返す。
func IsFuwaleteURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "fuwalete.com" || host == "www.fuwalete.com"
}

// FuwaleteSlug は記事の URL(`https://fuwalete.com/ito-365549/`)から slug を取り出す。
func FuwaleteSlug(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("URL の形式が不正です: %w", err)
	}
	slug := strings.Trim(u.Path, "/")
	if slug == "" || strings.Contains(slug, "/") {
		return "", fmt.Errorf("ふわりと将棋の棋譜速報の記事の URL ではありません(https://fuwalete.com/{記事}/ の形を貼ってください): %s", raw)
	}
	return slug, nil
}

// FuwaleteGame は「ふわりと将棋」から取得した 1 局分。
//
// **原本はサイトに書かれた指し手をそのまま写した KI2**(`KI2()`)。移動元を割り出す前の
// ものなので推測が入らない。⚠️ **誤記があっても全部の手を残す**(途中で切らない)。
// 盤に当てられるかどうかは別の話で、使う側がエラーの印として見る(`kicho.inspectBody`)。
type FuwaleteGame struct {
	SourceID  string // 記事の slug("ito-365549")
	Event     string // 記事の題から("王将戦 挑決L")。指し直し局なら "（指し直し）" を付ける
	Black     string
	White     string
	StartedAt time.Time
	TimeLimit time.Duration
	// EndMark はサイトに書かれた終局(例 "投了")。対局中は空。
	// **サイトの言い分**であって、指し手が盤に当てられるかとは関係ない。
	EndMark string
	// Moves はサイトの指し手(1 手目から最後の手まで)。欠けた手は "▲？" の置き場。
	Moves []kifu.KI2Move
}

// Finished は終局しているかどうかを返す。
func (g FuwaleteGame) Finished() bool { return g.EndMark != "" }

// KI2Document は原本の KI2 にする。
//
// 消費時間は書き方が揃っていない(残り時間だけ・"18/0:52"・"2" など)ので書かない。
func (g FuwaleteGame) KI2Document() kifu.KI2Document {
	return kifu.KI2Document{
		Document: kifu.Document{
			Handicap:  kifu.HirateHandicap,
			Event:     g.Event,
			StartedAt: g.StartedAt,
			Black:     g.Black,
			White:     g.White,
			TimeLimit: g.TimeLimit,
		},
		Moves: g.Moves,
		End:   g.EndMark,
	}
}

// KI2 は原本(KI2 テキスト)を返す。
func (g FuwaleteGame) KI2() string { return g.KI2Document().String() }

// FetchKifu は slug から KIF 本文を返す(HTTP のライブ経路用。⚠️ **ここは KIF で配る**)。
//
// 原本(KI2)を `format.KI2ToKIF` で変換する。盤に当てられない手があれば、そこまでの KIF と
// 止まった理由のコメントになる(保存済みの `/kifu/{id}.kif` と同じ文面)。
func (s *Fuwalete) FetchKifu(ctx context.Context, slug string) (string, error) {
	g, err := s.FetchGame(ctx, slug)
	if err != nil {
		return "", err
	}
	return format.KI2ToKIF(g.KI2())
}

// wpPost は REST API の記事(使うものだけ)。
type wpPost struct {
	Date  string `json:"date"` // サイトの時刻(JST)。タイムゾーンは付かない
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
}

// FetchGame は記事の slug から 1 局分を取得する(記事と、対局中なら共有の枠の 2 回)。
//
// ⚠️ 返す値は `MinInterval` の内側の呼び出しで共有される。**書き換えないこと。**
func (s *Fuwalete) FetchGame(ctx context.Context, slug string) (*FuwaleteGame, error) {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if slug == "" {
		return nil, fmt.Errorf("scrape: empty fuwalete slug")
	}
	return s.recent.do(ctx, s.MinInterval, slug, func(ctx context.Context) (*FuwaleteGame, error) {
		return s.fetchGame(ctx, slug)
	})
}

func (s *Fuwalete) fetchGame(ctx context.Context, slug string) (*FuwaleteGame, error) {
	post, err := s.fetchPost(ctx, "posts", slug)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, fmt.Errorf("ふわりと将棋に記事が見つかりません: %s", s.PostURL(slug))
	}

	// 対局中は最新の手が共有の枠(別のページ)にある。記事の手より新しいので先に並べる。
	var live string
	if liveSlug, ok := fuwaleteLiveSlug(post.Content.Rendered); ok {
		page, err := s.fetchPost(ctx, "pages", liveSlug)
		if err != nil {
			return nil, err
		}
		if page != nil {
			live = page.Content.Rendered
		}
	}

	g, err := parseFuwalete(post, live)
	if err != nil {
		return nil, fmt.Errorf("ふわりと将棋 %s: %w", slug, err)
	}
	g.SourceID = slug
	return g, nil
}

// fetchPost は REST API で slug の記事(kind は "posts" / "pages")を取る。無ければ nil。
func (s *Fuwalete) fetchPost(ctx context.Context, kind, slug string) (*wpPost, error) {
	q := url.Values{"slug": {slug}, "_fields": {"date,title,content"}}
	u := s.base() + "/wp-json/wp/v2/" + kind + "?" + q.Encode()
	b, err := getBytes(ctx, s.client(), u)
	if err != nil {
		return nil, fmt.Errorf("ふわりと将棋の記事を取得できませんでした: %w", err)
	}
	var posts []wpPost
	if err := json.Unmarshal(b, &posts); err != nil {
		return nil, fmt.Errorf("ふわりと将棋の応答が読めません(サイトの作りが変わった可能性があります): %w", err)
	}
	if len(posts) == 0 {
		return nil, nil
	}
	return &posts[0], nil
}

// fuwaleteLiveURL は記事の中の「最新の手を読み込む JS」から読み込み先を拾う。
var fuwaleteLiveURL = regexp.MustCompile(`var\s+url\s*=\s*["'](https?://[^"']+)["']`)

// fuwaleteLiveSlug は対局中の記事から、最新の手が載る枠のページの slug を返す。
func fuwaleteLiveSlug(content string) (string, bool) {
	if !strings.Contains(content, "ajaxUpdate") {
		return "", false
	}
	m := fuwaleteLiveURL.FindStringSubmatch(content)
	if m == nil || !IsFuwaleteURL(m[1]) {
		return "", false
	}
	slug, err := FuwaleteSlug(m[1])
	if err != nil {
		return "", false
	}
	return slug, true
}

var (
	// fuwaleteMoveLine は指し手の行。評価値は空のこともある("12手　%　")。
	// 詰みが見つかると評価値の欄が「詰みまでの手数」になる("102手　▲勝23手　△同銀")。
	// ⚠️ 欄も手番記号で始まるので、評価値として先に食べないと指し手に読まれる。
	fuwaleteMoveLine = regexp.MustCompile(`^(\d+)手[\s　]+(?:(?:\d*%|[▲△☗☖]勝[0-9０-９]+手)[\s　]+)?([▲△☗☖].*)$`)
	// fuwaleteEndLine は終局の行("△投了　18時8分")。
	fuwaleteEndLine = regexp.MustCompile(`^[▲△☗☖]([^\s　]+)`)
	fuwaleteBlack   = regexp.MustCompile(`先手[▲☗]([^\s　]+)`)
	fuwaleteWhite   = regexp.MustCompile(`後手[△☖]([^\s　]+)`)
	fuwaleteLimit   = regexp.MustCompile(`持ち時間[：:][\s　]*([^\s　]+)`)
	// fuwaleteTitle は記事の題 "11.6(金) 王将戦 挑決L 伊藤匠 永瀬拓矢 10時～棋譜速報"。
	fuwaleteTitle = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})[\s　]*[(（][^)）]*[)）][\s　]*(.*)$`)
	fuwaleteHour  = regexp.MustCompile(`(\d{1,2})時`)
)

// fuwaleteText は本文の指し手まわりを読んだもの。
type fuwaleteText struct {
	moves    map[int]string   // 手数 → KI2("▲55金")
	comments map[int][]string // 手数 → 解説の行
	end      string           // 終局の行の終局名("投了")
	black    string           // 見出しの先手("永瀬九段")
	white    string
	limit    time.Duration
	// rematch は指し直しの区切りがあったか(読んだのは指し直し局)。
	rematch bool
}

// readFuwaleteText は本文(HTML)を行にして、指し手・解説・終局・先後・持ち時間を拾う。
//
// 同じ手数が 2 度出たら**先に出たほうを使う**(枠の最新の手を記事より先に読ませるため)。
// ⚠️ 同じ手数で中身が違えばエラー(どちらが正しいか分からない)。
//
// 千日手などの指し直しがあると、1 つの記事に 2 局が並ぶ(上が指し直し局、
// `ーー 指し直し 開始 ーー` の下が前の局。先後も入れ替わる)。**読むのは区切りより上だけ**
// (`isFuwaleteRematchLine`)。下まで読むと、同じ手数に別の局の手が来てエラーになる。
func readFuwaleteText(contents ...string) (*fuwaleteText, error) {
	t := &fuwaleteText{moves: map[int]string{}, comments: map[int][]string{}}
	for _, c := range contents {
		last := 0 // 直前の行が指し手ならその手数(解説を付ける先)
		for _, line := range htmlLines(c) {
			trimmed := strings.Trim(line, " \t　")
			if isFuwaleteRematchLine(trimmed) {
				t.rematch = true
				break // ここから下は前の局
			}
			if m := fuwaleteMoveLine.FindStringSubmatch(trimmed); m != nil {
				num, _ := strconv.Atoi(m[1])
				move := fuwaleteMoveText(m[2])
				if prev, ok := t.moves[num]; ok {
					if prev != move {
						return nil, fmt.Errorf("%d手目が 2 通り書かれています(%s / %s)", num, prev, move)
					}
					last = 0
					continue
				}
				t.moves[num] = move
				last = num
				continue
			}
			switch {
			case trimmed == "":
				last = 0
			case last != 0 && strings.HasPrefix(line, "　") && !strings.Contains(trimmed, "ーー"):
				// 指し手の行のすぐ後の字下げした行はその手の解説。
				t.comments[last] = append(t.comments[last], trimmed)
			default:
				last = 0
				if m := fuwaleteEndLine.FindStringSubmatch(trimmed); m != nil && t.end == "" {
					if marker, ok := kifu.TerminalMarker(m[1]); ok {
						t.end = marker
					}
				}
				if m := fuwaleteBlack.FindStringSubmatch(trimmed); m != nil && t.black == "" {
					t.black = m[1]
				}
				if m := fuwaleteWhite.FindStringSubmatch(trimmed); m != nil && t.white == "" {
					t.white = m[1]
				}
				if m := fuwaleteLimit.FindStringSubmatch(trimmed); m != nil && t.limit == 0 {
					t.limit = parseShogiDB2Time(m[1])
				}
			}
		}
	}
	return t, nil
}

// fuwaleteRematchSuffix は指し直し局の棋戦名に付ける印("順位戦 A級（指し直し）")。
const fuwaleteRematchSuffix = "（指し直し）"

// isFuwaleteRematchLine は指し直しの区切り(`ーー 指し直し 開始 ーー`)の行かどうかを返す。
// 見出しの `指し直し 棋譜速報 自動更新　終了` は区切りではない(`ーー` が無い)。
func isFuwaleteRematchLine(trimmed string) bool {
	return strings.Contains(trimmed, "ーー") && strings.Contains(trimmed, "指し直し")
}

// fuwaleteMoveText は指し手の行の手番記号から後ろ("▲同玉 9/1:10")から指し手だけを取る。
// "▲同　玉" のように「同」の後に空白を入れて書かれても 1 手として読む。
//
// 付記が空白なしで続いていても(`▲65角26歩` は `▲65角 ai26歩` の書き損じ)指し手だけを取る
// (`cutFuwaleteNote`)。
func fuwaleteMoveText(s string) string {
	f := strings.Fields(s)
	move := f[0]
	if len(f) >= 2 && strings.HasSuffix(f[0], "同") {
		move = f[0] + f[1]
	}
	return cutFuwaleteNote(move)
}

// cutFuwaleteNote は指し手("▲65角26歩")の移動先より後ろに数字か英字が来たら、そこから先を
// 空白の抜けた付記(AI の推奨手・時間)として落とす。
//
// KI2 の 1 手は移動先(`65` か `同`)のあとに駒名と修飾(右・引・成・打 など)しか来ないので、
// 数字と英字は指し手の一部になりえない。**直すのは行の区切り方であって指し手ではない。**
// 残った指し手が誤記なら、そのままエラーの印になる。
func cutFuwaleteNote(move string) string {
	rs := []rune(move)
	start := 3 // 手番記号 + 移動先 2 文字
	if len(rs) > 1 && rs[1] == '同' {
		start = 2
	}
	for i := start; i < len(rs); i++ {
		r := rs[i]
		if r >= '0' && r <= '9' || r >= '０' && r <= '９' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return string(rs[:i])
		}
	}
	return move
}

// htmlLines は本文の HTML を表示どおりの行に分ける(`<br>` と段落・見出しで改行)。
// script / style の中身は捨てる。
func htmlLines(content string) []string {
	var sb strings.Builder
	z := html.NewTokenizer(strings.NewReader(content))
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.Split(sb.String(), "\n")
		case html.TextToken:
			if skip == 0 {
				sb.WriteString(strings.ReplaceAll(string(z.Text()), "\n", ""))
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style":
				skip++
			case "br", "p", "div", "h1", "h2", "h3", "h4", "li", "tr", "figure", "table":
				sb.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style":
				if skip > 0 {
					skip--
				}
			case "p", "div", "h1", "h2", "h3", "h4", "li", "tr", "figure", "table":
				sb.WriteByte('\n')
			}
		}
	}
}

// parseFuwalete は記事(と対局中なら枠のページ)から 1 局分を組み立てる。
func parseFuwalete(post *wpPost, live string) (*FuwaleteGame, error) {
	title := parseFuwaleteTitle(html.UnescapeString(post.Title.Rendered), post.Date)

	// 枠は共有なので、別の対局のものを掴んでいないか対局者で確かめる。
	if live != "" {
		lt, err := readFuwaleteText(live)
		if err != nil || !title.hasPlayer(lt.black) || !title.hasPlayer(lt.white) {
			live = ""
		}
	}
	contents := []string{post.Content.Rendered}
	if live != "" {
		contents = []string{live, post.Content.Rendered}
	}
	t, err := readFuwaleteText(contents...)
	if err != nil {
		return nil, err
	}

	event := title.event
	if t.rematch {
		// 指し直し局を表す欄が無いので棋戦名に付ける(検索でも当たる)。
		event += fuwaleteRematchSuffix
	}
	g := &FuwaleteGame{
		Event:     event,
		Black:     title.fullName(t.black),
		White:     title.fullName(t.white),
		StartedAt: title.startedAt,
		TimeLimit: t.limit,
	}

	// 1 手目から最後の手までを全部写す(誤記があっても切らない)。欠けた手数には置き場を入れる。
	last := 0
	for n := range t.moves {
		last = max(last, n)
	}
	for n := 1; n <= last; n++ {
		comment := strings.Join(t.comments[n], "\n")
		text, ok := t.moves[n]
		if !ok {
			mark := "▲"
			if n%2 == 0 {
				mark = "△"
			}
			text = mark + "？"
			comment = fmt.Sprintf("ふわりと将棋に %d 手目がありません", n)
		}
		g.Moves = append(g.Moves, kifu.KI2Move{Text: kifu.NormalizeKI2Move(text), Comment: comment})
	}
	g.EndMark = t.end
	return g, nil
}

// fuwaleteTitleInfo は記事の題から読んだもの。
type fuwaleteTitleInfo struct {
	event     string
	players   []string // 題に書かれた対局者のフルネーム(先後は分からない)
	startedAt time.Time
}

// parseFuwaleteTitle は "11.6(金) 王将戦 挑決L 伊藤匠 永瀬拓矢 10時～棋譜速報" を読む。
// 年は題に無いので記事の投稿日から取る(年をまたぐ前日の投稿なら翌年)。
func parseFuwaleteTitle(title, postDate string) fuwaleteTitleInfo {
	var info fuwaleteTitleInfo
	m := fuwaleteTitle.FindStringSubmatch(strings.TrimSpace(title))
	if m == nil {
		info.event = strings.TrimSpace(title)
		return info
	}
	fields := strings.Fields(m[3])
	hour := -1
	if n := len(fields); n > 0 && strings.Contains(fields[n-1], "棋譜速報") {
		if h := fuwaleteHour.FindStringSubmatch(fields[n-1]); h != nil {
			hour, _ = strconv.Atoi(h[1])
		}
		fields = fields[:n-1]
	}
	if n := len(fields); n >= 3 {
		info.players = fields[n-2:]
		fields = fields[:n-2]
	}
	info.event = strings.Join(fields, " ")

	month, _ := strconv.Atoi(m[1])
	day, _ := strconv.Atoi(m[2])
	posted, err := time.ParseInLocation("2006-01-02T15:04:05", postDate, jst)
	if err == nil && hour >= 0 {
		year := posted.Year()
		if month < int(posted.Month())-6 {
			year++
		}
		info.startedAt = time.Date(year, time.Month(month), day, hour, 0, 0, 0, jst)
	}
	return info
}

// fullName は見出しの呼び名("伊藤叡王")を題のフルネーム("伊藤匠")に直す。
// 姓(先頭の一致)で当て、当たらなければ呼び名のまま。
func (t fuwaleteTitleInfo) fullName(short string) string {
	best, bestLen := short, 0
	for _, p := range t.players {
		if n := commonPrefixRunes(p, short); n > bestLen {
			best, bestLen = p, n
		}
	}
	return best
}

// hasPlayer は呼び名が題の対局者の誰かに当たるかを返す(題から対局者が読めなければ true)。
func (t fuwaleteTitleInfo) hasPlayer(short string) bool {
	if len(t.players) == 0 {
		return true
	}
	for _, p := range t.players {
		if commonPrefixRunes(p, short) > 0 {
			return true
		}
	}
	return false
}

func commonPrefixRunes(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := 0
	for n < len(ra) && n < len(rb) && ra[n] == rb[n] {
		n++
	}
	return n
}
