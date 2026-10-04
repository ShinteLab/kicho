package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/store"
)

// KifuService は棋譜の取得・保存・削除をフロントに公開する。
//
// **ロジックは kicho.Library 側にあり、ここは DTO の変換と入力チェックだけ。**
// 取得元ごとの諸元(source_url)の決め方も、文字コードの既定も、手数の数え方も
// 書かないこと —— **同じライブラリを ikkyoku も使う**ので、ここに書くと
// あちらと黙って挙動が割れる(kicho.Library / kicho.Fetched に寄せてある)。
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

// 操作ごとの制限時間。
//
// ⚠️ **context.Background() をそのまま DB へ渡さないこと。** store は接続を
// 1本に絞っているので、止まらないクエリが1つあると以後の操作が全部待たされる。
const (
	dbTimeout  = 10 * time.Second
	netTimeout = 60 * time.Second
)

func dbContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), dbTimeout)
}

func netContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), netTimeout)
}

// GameSummary は一覧表示用の1件。
type GameSummary struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	SourceID string `json:"sourceId"`
	// SourceURL は取得元の URL。
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

// fetchedToDetail は kicho.Fetched を画面用に変換する(表示のための整形だけ)。
func fetchedToDetail(f kicho.Fetched) GameDetail {
	d := GameDetail{
		GameSummary: GameSummary{
			Source:    f.Source,
			SourceID:  f.SourceID,
			SourceURL: f.SourceURL,
			Event:     f.Event,
			Handicap:  f.Handicap,
			Place:     f.Place,
			Black:     f.Black,
			White:     f.White,
			EndMark:   f.EndMark,
			Finished:  f.Finished(),
			Moves:     f.Moves,
		},
		KIF:      f.KIF,
		Encoding: f.Encoding,
	}
	if !f.StartedAt.IsZero() {
		d.StartedAt = f.StartedAt.Format(time.RFC3339)
	}
	return d
}

// detailToFetched は画面の内容を保存できる形に戻す。
//
// **諸元(source_url)と手数は Library.Save が決め直す**ので、ここでは運ぶだけ。
func detailToFetched(d GameDetail) kicho.Fetched {
	f := kicho.Fetched{
		Source:    d.Source,
		SourceID:  d.SourceID,
		SourceURL: d.SourceURL,
		Event:     d.Event,
		Handicap:  d.Handicap,
		Place:     d.Place,
		Black:     d.Black,
		White:     d.White,
		EndMark:   d.EndMark,
		Moves:     d.Moves,
		KIF:       d.KIF,
		Encoding:  d.Encoding,
	}
	if d.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, d.StartedAt); err == nil {
			f.StartedAt = t
		}
	}
	return f
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
	// Limit は取得件数の上限(0 なら MaxSearchRows)。
	Limit int `json:"limit"`
	// Offset は読み飛ばす件数。
	Offset int `json:"offset"`
}

// SearchResult は検索結果と件数。
//
// **件数を2つ返すのは UI が「N 件中 M 件」を出すため。** Search と Count を
// 別々に呼ぶのでは、条件付きの該当件数が取れない。
type SearchResult struct {
	Games []GameSummary `json:"games"`
	// Matched は条件に合う件数(上限で切る前)。
	Matched int `json:"matched"`
	// Total は蔵書全体の件数。
	Total int `json:"total"`
	// Truncated は上限で切ったかどうか。
	Truncated bool `json:"truncated"`
	// Limit は実際に適用した上限。UI が「先頭 N 件」と出すのに使う。
	Limit int `json:"limit"`
}

// MinSearchLength は索引が効く最小文字数(UI の案内表示に使う)。
const MinSearchLength = store.MinTrigramLen

// MaxSearchRows は一覧が1回に返す上限(UI の案内表示に使う)。
const MaxSearchRows = kicho.MaxSearchRows

// jst は日付入力の解釈に使うタイムゾーン。
var jst = time.FixedZone("JST", 9*60*60)

// List は保存済み棋譜の一覧を新しい順に返す(KIF 本文は含まない)。
func (s *KifuService) List() (SearchResult, error) {
	return s.Search(SearchQuery{})
}

// Search は条件に合う棋譜を新しい順に返す(KIF 本文は含まない)。
func (s *KifuService) Search(q SearchQuery) (SearchResult, error) {
	sq := store.Query{
		Text:         q.Text,
		FinishedOnly: q.FinishedOnly,
		Limit:        q.Limit,
		Offset:       q.Offset,
	}

	// 日付は JST の 0:00 / 23:59:59 として解釈する。
	if q.From != "" {
		t, err := time.ParseInLocation("2006-01-02", q.From, jst)
		if err != nil {
			return SearchResult{}, fmt.Errorf("開始日の形式が不正です: %s", q.From)
		}
		sq.From = t
	}
	if q.To != "" {
		t, err := time.ParseInLocation("2006-01-02", q.To, jst)
		if err != nil {
			return SearchResult{}, fmt.Errorf("終了日の形式が不正です: %s", q.To)
		}
		sq.To = t.Add(24*time.Hour - time.Second)
	}
	if !sq.From.IsZero() && !sq.To.IsZero() && sq.To.Before(sq.From) {
		return SearchResult{}, fmt.Errorf("終了日が開始日より前になっています")
	}

	ctx, cancel := dbContext()
	defer cancel()

	res, err := s.lib.Search(ctx, sq)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{
		Games:     make([]GameSummary, 0, len(res.Games)),
		Matched:   res.Matched,
		Total:     res.Total,
		Truncated: res.Truncated,
		Limit:     MaxSearchRows,
	}
	for _, r := range res.Games {
		out.Games = append(out.Games, toSummary(r))
	}
	return out, nil
}

// Get は棋譜1件を KIF 本文つきで返す。
func (s *KifuService) Get(id string) (GameDetail, error) {
	ctx, cancel := dbContext()
	defer cancel()

	r, err := s.lib.Get(ctx, id)
	if err != nil {
		return GameDetail{}, err
	}
	return GameDetail{GameSummary: toSummary(r), KIF: r.Body, Encoding: r.Encoding}, nil
}

// Delete は棋譜を削除する。
func (s *KifuService) Delete(id string) error {
	ctx, cancel := dbContext()
	defer cancel()

	return s.lib.Delete(ctx, id)
}

// Fetch はライブ中継から棋譜を取得する(保存はしない)。
//
// 取得元は入力から判別する(判別も取得も kicho 側。kicho.Fetcher.Fetch を参照)。
//
//   - live.shogi.or.jp の URL  → 日本将棋連盟の棋譜中継
//   - shogidb2.com の URL      → 将棋DB2
//   - yomiuri.co.jp の URL     → 読売(竜王戦)
//   - それ以外の http(s) URL   → その中身を .kif として読む(HTML なら辿る)
//   - URL でない文字列         → 読売の棋譜 ID
//
// 対局中の棋譜も取得できる(その場合 Finished は false)。
func (s *KifuService) Fetch(input string) (GameDetail, error) {
	ctx, cancel := netContext()
	defer cancel()

	f, err := s.lib.Fetch(ctx, input)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
}

// Refresh は取得済みのカードを取り直す。
//
// 入力欄からの Fetch と違って取得元が分かっているので、判別も
// 中継ページ→棋譜 ID の往復も挟まらず、棋譜 ID で直接取りに行く。
func (s *KifuService) Refresh(source, sourceID string) (GameDetail, error) {
	ctx, cancel := netContext()
	defer cancel()

	f, err := s.lib.Refresh(ctx, source, sourceID)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
}

// Save は Fetch で取得して画面に表示している内容をそのまま保存する。
// サイトへ取り直しには行かない。
//
// 同じ棋譜を保存し直しても重複せず、既存の ID を維持したまま内容が更新される
// (対局中に保存 → 終局後に取り直して保存、で同じ ID のまま最新になる)。
func (s *KifuService) Save(d GameDetail) (GameSummary, error) {
	ctx, cancel := dbContext()
	defer cancel()

	rec, err := s.lib.Save(ctx, detailToFetched(d))
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// PreviewKIF は KIF テキストを解析して内容を返す(保存はしない)。
// 貼り付け登録の前に、何が読み取れたかを確認するために使う。
func (s *KifuService) PreviewKIF(text string) (GameDetail, error) {
	f, err := s.lib.PreviewKIF(text)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
}

// ImportKIF は KIF テキストを解析して保存する(貼り付け登録)。
// 取得元での一意な ID が無いため、毎回新しい棋譜として登録される。
func (s *KifuService) ImportKIF(text string) (GameSummary, error) {
	ctx, cancel := dbContext()
	defer cancel()

	rec, err := s.lib.ImportKIF(ctx, text)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// PreviewURL は URL から KIF を取得して解析する(保存はしない)。
func (s *KifuService) PreviewURL(rawURL string) (GameDetail, error) {
	ctx, cancel := netContext()
	defer cancel()

	f, err := s.lib.PreviewURL(ctx, rawURL)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
}

// ImportURL は URL から KIF を取得して保存する。
func (s *KifuService) ImportURL(rawURL string) (GameSummary, error) {
	ctx, cancel := netContext()
	defer cancel()

	rec, err := s.lib.ImportURL(ctx, rawURL)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// WatchEntry は仮の一覧(追跡中の中継)1件。
//
// **KIF 本文を持たない。** ここが持つのは「どのサイトのどの棋譜か」と、
// 一覧で見分けるためのメタだけ。復元したカードの中身はユーザが
// 「更新」を押した時点で Refresh が取りに行く。
type WatchEntry struct {
	Source   string `json:"source"`
	SourceID string `json:"sourceId"`
	// SourceURL は人が開いて確認できる URL。
	SourceURL string `json:"sourceUrl"`
	Event     string `json:"event"`
	Black     string `json:"black"`
	White     string `json:"white"`
	StartedAt string `json:"startedAt"` // RFC3339。未設定なら空文字
	EndMark   string `json:"endMark"`
	Finished  bool   `json:"finished"`
	// Moves は最後に取得したときの手数。
	Moves int `json:"moves"`
}

func toWatchEntry(w store.Watch) WatchEntry {
	e := WatchEntry{
		Source:    w.Source,
		SourceID:  w.SourceID,
		SourceURL: w.SourceURL,
		Event:     w.Event,
		Black:     w.Black,
		White:     w.White,
		EndMark:   w.EndMark,
		Finished:  w.Finished(),
		Moves:     w.Moves,
	}
	if !w.StartedAt.IsZero() {
		e.StartedAt = w.StartedAt.Format(time.RFC3339)
	}
	return e
}

// Watches は仮の一覧を新しい順に返す(取得タブの復元に使う)。
//
// **ここからサイトへは取りに行かない。** 起動のたびに追跡ぶんの通信が
// 走らないよう、並べるところまでで止める。
func (s *KifuService) Watches() ([]WatchEntry, error) {
	ctx, cancel := dbContext()
	defer cancel()

	list, err := s.lib.Watches(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WatchEntry, 0, len(list))
	for _, w := range list {
		out = append(out, toWatchEntry(w))
	}
	return out, nil
}

// Watch は取得したカードを仮の一覧に載せる(既にあれば最新化する)。
//
// 2日制の対局で翌日また中継の URL を貼り直さずに済むようにするためのもの。
// 取り直せるものだけが対象で、貼り付け(paste)は取得元が無いので kicho 側が弾く
// (url は sourceId が URL そのものなので取り直せる)。
func (s *KifuService) Watch(d GameDetail) (WatchEntry, error) {
	ctx, cancel := dbContext()
	defer cancel()

	w, err := s.lib.Watch(ctx, detailToFetched(d))
	if err != nil {
		return WatchEntry{}, err
	}
	return toWatchEntry(w), nil
}

// Unwatch は仮の一覧から外す(保存済みの棋譜は消えない)。
func (s *KifuService) Unwatch(source, sourceID string) error {
	ctx, cancel := dbContext()
	defer cancel()

	err := s.lib.Unwatch(ctx, source, sourceID)
	if errors.Is(err, store.ErrNotFound) {
		// 画面から消すのが目的なので、既に無いのは失敗にしない
		// (終局後の保存で Library が先に外していることがある)。
		return nil
	}
	return err
}

// UnwatchAll は仮の一覧を空にする(取得タブの「クリア」)。消した件数を返す。
func (s *KifuService) UnwatchAll() (int, error) {
	ctx, cancel := dbContext()
	defer cancel()

	return s.lib.UnwatchAll(ctx)
}
