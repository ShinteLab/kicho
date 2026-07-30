package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/format"
	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// KifuService は棋譜の取得・保存・削除をフロントに公開する。
// ロジックは shinte/kicho の Library 側にあり、ここは変換と入力チェックだけ行う。
//
// 取得(Fetch)と保存(Save)は別操作にしてある。対局中の棋譜は随時更新されるため、
// 「取得して内容を確認 → その表示内容をそのまま保存」という流れにするのが目的で、
// 保存時にサイトへ取り直しに行くことはない。
type KifuService struct {
	lib *kicho.Library
}

func NewKifuService(lib *kicho.Library) *KifuService {
	return &KifuService{lib: lib}
}

// GameSummary は一覧表示用の1件。
type GameSummary struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	SourceID string `json:"sourceId"`
	// SourceURL は取得元の URL(URL 取り込みのみ)。
	SourceURL string `json:"sourceUrl"`
	Event     string `json:"event"`
	Handicap  string `json:"handicap"`
	Place     string `json:"place"`
	Black     string `json:"black"`
	White     string `json:"white"`
	StartedAt string `json:"startedAt"` // RFC3339。未設定なら空文字
	// EndMark は終局の種別(例 "投了")。対局中は空。
	EndMark string `json:"endMark"`
	// Finished は終局済みかどうか。UI で手数の横に「(終局)」を出すのに使う。
	Finished bool `json:"finished"`
	Moves    int  `json:"moves"`
}

// GameDetail は棋譜1件の詳細(KIF 本文つき)。
// Fetch が返したものをそのまま Save に渡せる。
type GameDetail struct {
	GameSummary
	KIF string `json:"kif"`
	// Encoding は KIF の元の文字コード(本文は UTF-8 に寄せてある)。
	// 連盟の中継は Shift_JIS なので、保存の記録として一緒に運ぶ。
	Encoding string `json:"encoding"`
}

func toSummary(r store.Record) GameSummary {
	s := GameSummary{
		ID:        r.ID,
		Source:    r.Source,
		SourceID:  r.SourceID,
		SourceURL: r.SourceURL,
		Event:     r.Event,
		Handicap:  r.Handicap,
		Place:     r.Place,
		Black:     r.Black,
		White:     r.White,
		EndMark:   r.EndMark,
		Finished:  r.Finished(),
		Moves:     r.Moves,
	}
	if !r.StartedAt.IsZero() {
		s.StartedAt = r.StartedAt.Format(time.RFC3339)
	}
	return s
}

// countMoves は KIF テキストの手数を数える。
// 手数は store に列として持たせてあるので、保存時にここで数えて渡す。
//
// 行数を数えるのではなく core/kifu で解析する。サイトが配信している .kif には
// コメント行(`*`)や `# --- Kifu for Windows ...` が混ざっており、
// 行を数えるとそれらまで手数に入ってしまうため。
func countMoves(kif string) int {
	doc, err := kicho.ParseKIF(kif)
	if err != nil {
		return 0
	}
	return len(doc.Moves)
}

// List は保存済み棋譜の一覧を新しい順に返す(KIF 本文は含まない)。
func (s *KifuService) List() ([]GameSummary, error) {
	return s.Search(SearchQuery{})
}

// SearchQuery は検索条件。空の項目は「条件なし」。
type SearchQuery struct {
	// Text は棋戦名・対局者・場所への部分一致。
	// 3文字以上なら索引(FTS5 trigram)が効く。それ未満は走査になる。
	Text string `json:"text"`
	// From / To は開始日の範囲。"YYYY-MM-DD" 形式。空なら無制限。
	From string `json:"from"`
	To   string `json:"to"`
	// FinishedOnly が true なら終局済みのみ。
	FinishedOnly bool `json:"finishedOnly"`
	// Limit は取得件数の上限(0 なら無制限)。
	Limit int `json:"limit"`
}

// MinSearchLength は索引が効く最小文字数(UI の案内表示に使う)。
const MinSearchLength = store.MinTrigramLen

// jst は日付入力の解釈に使うタイムゾーン。
var jst = time.FixedZone("JST", 9*60*60)

// Search は条件に合う棋譜を新しい順に返す(KIF 本文は含まない)。
func (s *KifuService) Search(q SearchQuery) ([]GameSummary, error) {
	sq := store.Query{
		Text:         q.Text,
		FinishedOnly: q.FinishedOnly,
		Limit:        q.Limit,
	}

	// 日付は JST の 0:00 / 23:59:59 として解釈する。
	if q.From != "" {
		t, err := time.ParseInLocation("2006-01-02", q.From, jst)
		if err != nil {
			return nil, fmt.Errorf("開始日の形式が不正です: %s", q.From)
		}
		sq.From = t
	}
	if q.To != "" {
		t, err := time.ParseInLocation("2006-01-02", q.To, jst)
		if err != nil {
			return nil, fmt.Errorf("終了日の形式が不正です: %s", q.To)
		}
		sq.To = t.Add(24*time.Hour - time.Second)
	}
	if !sq.From.IsZero() && !sq.To.IsZero() && sq.To.Before(sq.From) {
		return nil, fmt.Errorf("終了日が開始日より前になっています")
	}

	recs, err := s.lib.Store().Search(context.Background(), sq)
	if err != nil {
		return nil, err
	}
	out := make([]GameSummary, 0, len(recs))
	for _, r := range recs {
		out = append(out, toSummary(r))
	}
	return out, nil
}

// Count は保存件数を返す(検索結果と全体を比べて表示するため)。
func (s *KifuService) Count() (int, error) {
	return s.lib.Store().Count(context.Background())
}

// Get は棋譜1件を KIF 本文つきで返す。
func (s *KifuService) Get(id string) (GameDetail, error) {
	r, err := s.lib.Store().Get(context.Background(), id)
	if err != nil {
		return GameDetail{}, err
	}
	return GameDetail{GameSummary: toSummary(r), KIF: r.Body}, nil
}

// Delete は棋譜を削除する。
func (s *KifuService) Delete(id string) error {
	return s.lib.Store().Delete(context.Background(), id)
}

// Fetch はライブ中継から棋譜を取得する(保存はしない)。
//
// 取得元は入力から判別する。
//
//   - live.shogi.or.jp の URL      → 日本将棋連盟の棋譜中継
//   - それ以外(URL / 棋譜 ID)      → 読売(竜王戦)
//
// 対局中の棋譜も取得できる(その場合 Finished は false)。
func (s *KifuService) Fetch(input string) (GameDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if strings.TrimSpace(input) == "" {
		return GameDetail{}, fmt.Errorf("URL または棋譜 ID を入力してください")
	}
	if scrape.IsShogiLiveURL(input) {
		id, err := s.lib.ResolveShogiLiveInput(ctx, input)
		if err != nil {
			return GameDetail{}, err
		}
		return s.fetchShogiLive(ctx, id)
	}

	id, err := s.resolve(ctx, input)
	if err != nil {
		return GameDetail{}, err
	}
	return s.fetchRyuoh(ctx, id)
}

// Refresh は取得済みのカードを取り直す。
//
// 入力欄からの Fetch と違って取得元が分かっているので、判別も
// 中継ページ→棋譜 ID の往復も挟まらず、棋譜 ID で直接取りに行く。
func (s *KifuService) Refresh(source, sourceID string) (GameDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if strings.TrimSpace(sourceID) == "" {
		return GameDetail{}, fmt.Errorf("取得元の棋譜 ID が空です")
	}
	switch source {
	case store.SourceShogiLive:
		return s.fetchShogiLive(ctx, sourceID)
	case store.SourceYomiuri, "":
		return s.fetchRyuoh(ctx, sourceID)
	default:
		return GameDetail{}, fmt.Errorf("取り直せない取得元です: %s", source)
	}
}

// fetchRyuoh は読売のペイロードから KIF を組み立てる。
func (s *KifuService) fetchRyuoh(ctx context.Context, id string) (GameDetail, error) {
	g, err := s.lib.FetchRyuoh(ctx, id)
	if err != nil {
		return GameDetail{}, err
	}

	d := GameDetail{
		GameSummary: GameSummary{
			Source:   store.SourceYomiuri,
			SourceID: g.SourceID,
			Event:    g.Event,
			Handicap: g.Handicap,
			Place:    g.Place,
			Black:    g.Black,
			White:    g.White,
			EndMark:  g.EndMark,
			Finished: g.Finished(),
			Moves:    len(g.Moves),
		},
		// スクレイピングは構造化データから組み立てるので、これ自体が原本。
		KIF:      g.KIF(),
		Encoding: kicho.EncodingUTF8,
	}
	if !g.StartedAt.IsZero() {
		d.StartedAt = g.StartedAt.Format(time.RFC3339)
	}
	return d, nil
}

// fetchShogiLive は連盟の中継から .kif を取る。
//
// 本文は**サイトが配信している原本のまま**渡す(整形し直さない)。
// 画面に出すメタデータだけ解析結果から取る。
func (s *KifuService) fetchShogiLive(ctx context.Context, id string) (GameDetail, error) {
	g, err := s.lib.FetchShogiLive(ctx, id)
	if err != nil {
		return GameDetail{}, err
	}

	end := g.Doc.EndMark()
	d := GameDetail{
		GameSummary: GameSummary{
			Source:   store.SourceShogiLive,
			SourceID: g.SourceID,
			// 諸元: どこから取ったか。人が開いて確認するのは中継ページ。
			SourceURL: s.lib.ShogiLiveViewerURL(g.SourceID),
			Event:     g.Doc.Event,
			Handicap:  g.Doc.Handicap,
			Place:     g.Doc.Place,
			Black:     g.Doc.Black,
			White:     g.Doc.White,
			EndMark:   end,
			Finished:  end != "",
			Moves:     len(g.Doc.Moves),
		},
		KIF:      g.KIF,
		Encoding: g.Encoding,
	}
	if !g.Doc.StartedAt.IsZero() {
		d.StartedAt = g.Doc.StartedAt.Format(time.RFC3339)
	}
	return d, nil
}

// Save は Fetch で取得して画面に表示している内容をそのまま保存する。
// サイトへ取り直しには行かない。
//
// 同じ棋譜を保存し直しても重複せず、既存の ID を維持したまま内容が更新される
// (対局中に保存 → 終局後に取り直して保存、で同じ ID のまま最新になる)。
func (s *KifuService) Save(d GameDetail) (GameSummary, error) {
	if d.SourceID == "" {
		return GameSummary{}, fmt.Errorf("保存する棋譜がありません。先に取得してください")
	}
	if d.KIF == "" {
		return GameSummary{}, fmt.Errorf("棋譜が空です")
	}

	// 取得元によって諸元(どこから取ったか)の組み立てが変わる。
	source, sourceURL := d.Source, d.SourceURL
	switch source {
	case store.SourceShogiLive:
		sourceURL = s.lib.ShogiLiveViewerURL(d.SourceID)
	case store.SourceYomiuri, "":
		// 取得元が入っていない古い画面状態でも読売として保存できるようにしておく。
		source = store.SourceYomiuri
		sourceURL = scrape.ViewerURL(d.SourceID)
	default:
		return GameSummary{}, fmt.Errorf("保存できない取得元です: %s", source)
	}

	encoding := d.Encoding
	if encoding == "" {
		encoding = kicho.EncodingUTF8
	}

	g := store.Game{
		Source:    source,
		SourceID:  d.SourceID,
		SourceURL: sourceURL,
		Event:     d.Event,
		Handicap:  d.Handicap,
		Place:     d.Place,
		Black:     d.Black,
		White:     d.White,
		EndMark:   d.EndMark,
		Moves:     countMoves(d.KIF),

		// 画面に出している内容をそのまま書き込む(サイトへ取り直しには行かない)。
		Body:     d.KIF,
		Format:   string(format.KIF),
		Encoding: encoding,
	}
	if d.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, d.StartedAt); err == nil {
			g.StartedAt = t
		}
	}

	rec, err := s.lib.Store().Save(context.Background(), g)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// PreviewKIF は KIF テキストを解析して内容を返す(保存はしない)。
// 貼り付け登録の前に、何が読み取れたかを確認するために使う。
func (s *KifuService) PreviewKIF(text string) (GameDetail, error) {
	doc, err := kicho.ParseKIF(text)
	if err != nil {
		return GameDetail{}, err
	}
	return docToDetail(doc, store.SourcePaste, "", text, kicho.EncodingUTF8), nil
}

// ImportKIF は KIF テキストを解析して保存する(貼り付け登録)。
// 取得元での一意な ID が無いため、毎回新しい棋譜として登録される。
func (s *KifuService) ImportKIF(text string) (GameSummary, error) {
	rec, err := s.lib.ImportKIF(context.Background(), text)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// PreviewURL は URL から KIF を取得して解析する(保存はしない)。
func (s *KifuService) PreviewURL(rawURL string) (GameDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	text, encoding, err := s.lib.FetchKIFFromURL(ctx, rawURL)
	if err != nil {
		return GameDetail{}, err
	}
	doc, err := kicho.ParseKIF(text)
	if err != nil {
		return GameDetail{}, err
	}
	return docToDetail(doc, store.SourceURL, rawURL, text, encoding), nil
}

// ImportURL は URL から KIF を取得して保存する。
func (s *KifuService) ImportURL(rawURL string) (GameSummary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rec, err := s.lib.ImportURL(ctx, rawURL)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// docToDetail は解析結果を表示用に変換する。
//
// 表示する KIF は解析結果を組み立て直したものではなく **原本 (body) をそのまま**使う。
// 保存されるのも原本なので、画面と保存内容を食い違わせないため
// (組み立て直すと変化・コメント・不成などが落ちる)。
func docToDetail(doc kifu.Document, source, sourceURL, body, encoding string) GameDetail {
	end := doc.EndMark()
	d := GameDetail{
		GameSummary: GameSummary{
			Source:    source,
			SourceURL: sourceURL,
			Event:     doc.Event,
			Handicap:  doc.Handicap,
			Place:     doc.Place,
			Black:     doc.Black,
			White:     doc.White,
			EndMark:   end,
			Finished:  end != "",
			Moves:     len(doc.Moves),
		},
		KIF:      body,
		Encoding: encoding,
	}
	if !doc.StartedAt.IsZero() {
		d.StartedAt = doc.StartedAt.Format(time.RFC3339)
	}
	return d
}

// resolve は入力(対局ページ URL / 棋譜ビューア URL / 棋譜 ID)を棋譜 ID に解決する。
func (s *KifuService) resolve(ctx context.Context, input string) (string, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return "", fmt.Errorf("URL または棋譜 ID を入力してください")
	}
	if !strings.HasPrefix(in, "http://") && !strings.HasPrefix(in, "https://") {
		return in, nil // 棋譜 ID とみなす
	}
	// 棋譜ビューアの URL が直接貼られた場合はそこから ID を取る。
	if id, err := kicho.KifuIDFromURL(in); err == nil {
		return id, nil
	}
	// 対局ページ URL → iframe を辿って棋譜 ID を得る。
	return s.lib.ResolveRyuohURL(ctx, in)
}
