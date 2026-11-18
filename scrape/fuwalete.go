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
)

// Fuwalete は「ふわりと将棋」(fuwalete.com)の棋譜速報を扱う。
//
// **人が手で書いているブログ記事を読む。** サイトは KIF も構造化データも配っておらず、
// 指し手は記事の本文に**新しい手が上**の順で、**移動元を書かない KI2 の表記**で並ぶ:
//
//	97手　92%　▲55金 0:37
//	87手　89%　▲53銀引成 0:46
//
// そのため盤を開始局面から進めながら、どの駒が来たのかを割り出して
// （`kifu.DecodeKI2`）**kicho が KIF を組み立てる**。これが原本になる（読売・将棋DB2 と同じ）。
//
// ⚠️ **手入力なので誤記がある**（実例: 伊藤–永瀬戦の 74 手目「△89飛」は △29飛打 の誤り。
// `_docs/sources/fuwalete.md`）。**推測で直さない**。盤に当てられない手が出たら
// その前の手までを返し、どこで止まったかを棋譜の先頭のコメントに書く
// （サイトが直せば取り直しで最新化される）。
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
}

// FuwaleteBaseURL は「ふわりと将棋」の基点。
const FuwaleteBaseURL = "https://fuwalete.com"

// NewFuwalete は既定の HTTP クライアントを持つ Fuwalete を返す。
func NewFuwalete() *Fuwalete {
	return &Fuwalete{Client: &http.Client{Timeout: 30 * time.Second}}
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
type FuwaleteGame struct {
	SourceID  string // 記事の slug("ito-365549")
	Event     string // 記事の題から("王将戦 挑決L")
	Black     string
	White     string
	StartedAt time.Time
	TimeLimit time.Duration
	// EndMark は終局の種別(例 "投了")。対局中・途中で読めなくなったときは空。
	EndMark string
	// Moves は終局の手(EndMark)を含む。
	Moves []kifu.Move
	// Note は途中で読めなくなったときの説明(KIF の先頭のコメントになる)。読めたら空。
	Note string
}

// Finished は終局しているかどうかを返す。
func (g FuwaleteGame) Finished() bool { return g.EndMark != "" }

// Document は core/kifu の Document に変換する。
//
// 消費時間は書き方が揃っていない(残り時間だけ・"18/0:52"・"2" など)ので
// **ShowTime は立てない**。
func (g FuwaleteGame) Document() kifu.Document {
	return kifu.Document{
		Handicap:  kifu.HirateHandicap,
		Event:     g.Event,
		StartedAt: g.StartedAt,
		Black:     g.Black,
		White:     g.White,
		TimeLimit: g.TimeLimit,
		Comment:   g.Note,
		Moves:     g.Moves,
	}
}

// KIF は KIF テキストに変換する。
func (g FuwaleteGame) KIF() string { return g.Document().String() }

// FetchKifu は slug から KIF 本文だけを返す(HTTP のライブ経路用)。
func (s *Fuwalete) FetchKifu(ctx context.Context, slug string) (string, error) {
	g, err := s.FetchGame(ctx, slug)
	if err != nil {
		return "", err
	}
	return g.KIF(), nil
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

// FetchGame は記事の slug から 1 局分を取得する。
func (s *Fuwalete) FetchGame(ctx context.Context, slug string) (*FuwaleteGame, error) {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if slug == "" {
		return nil, fmt.Errorf("scrape: empty fuwalete slug")
	}
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
	fuwaleteMoveLine = regexp.MustCompile(`^(\d+)手[\s　]+(?:\d*%[\s　]+)?([▲△☗☖].*)$`)
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
}

// readFuwaleteText は本文(HTML)を行にして、指し手・解説・終局・先後・持ち時間を拾う。
//
// 同じ手数が 2 度出たら**先に出たほうを使う**(枠の最新の手を記事より先に読ませるため)。
// ⚠️ 同じ手数で中身が違えばエラー(どちらが正しいか分からない)。
func readFuwaleteText(contents ...string) (*fuwaleteText, error) {
	t := &fuwaleteText{moves: map[int]string{}, comments: map[int][]string{}}
	for _, c := range contents {
		last := 0 // 直前の行が指し手ならその手数(解説を付ける先)
		for _, line := range htmlLines(c) {
			trimmed := strings.Trim(line, " \t　")
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

// fuwaleteMoveText は指し手の行の手番記号から後ろ("▲同玉 9/1:10")から指し手だけを取る。
// "▲同　玉" のように「同」の後に空白を入れて書かれても 1 手として読む。
func fuwaleteMoveText(s string) string {
	f := strings.Fields(s)
	if len(f) >= 2 && strings.HasSuffix(f[0], "同") {
		return f[0] + f[1]
	}
	return f[0]
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

	g := &FuwaleteGame{
		Event:     title.event,
		Black:     title.fullName(t.black),
		White:     title.fullName(t.white),
		StartedAt: title.startedAt,
		TimeLimit: t.limit,
	}

	// 1 手目から続いているところまでを読む。
	var names []string
	for n := 1; ; n++ {
		m, ok := t.moves[n]
		if !ok {
			break
		}
		names = append(names, m)
	}
	gapAt := 0
	if len(names) < len(t.moves) {
		gapAt = len(names) + 1
	}

	start, err := kifu.StartSFEN(kifu.HirateHandicap)
	if err != nil {
		return nil, err
	}
	texts, _, derr := kifu.DecodeKI2(start, names)
	for i, mt := range texts {
		g.Moves = append(g.Moves, kifu.Move{
			Num: i + 1, Name: mt.Name, FromX: mt.FromX, FromY: mt.FromY,
			Comment: strings.Join(t.comments[i+1], "\n"),
		})
	}
	switch {
	case derr != nil:
		bad := len(texts) + 1
		g.Note = fmt.Sprintf("ふわりと将棋の %d 手目「%s」を盤に当てられないため、%d 手目までです（サイトの誤記の可能性があります）",
			bad, names[bad-1], bad-1)
	case gapAt != 0:
		g.Note = fmt.Sprintf("ふわりと将棋に %d 手目が見つからないため、%d 手目までです", gapAt, gapAt-1)
	case t.end != "":
		g.EndMark = t.end
		g.Moves = append(g.Moves, kifu.Move{Num: len(g.Moves) + 1, Name: t.end})
	}
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
