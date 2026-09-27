package kicho

import (
	"strings"
	"time"

	"github.com/ShinteLab/core/kifu"

	"github.com/ShinteLab/kicho/format"
	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// Fetched は取得した棋譜1局分（**まだ保存していない**もの）。
//
// 取得元が違っても同じ形になる。読売は構造化データから組み立て、連盟は配信
// されている .kif が原本、という違いは `Library.Fetch` の内側で吸収する。
//
// ⚠️ **これがフロントへ渡す DTO の原型。** ここに寄せないと kicho の Wails
// サービスと ikkyoku の KifuService が同じ変換（取得元ごとの source_url の決め方、
// 文字コードの既定、手数の数え方）をそれぞれ持つことになり、片方だけ直せば
// 黙って挙動が割れる。**取得元の知識は kicho に置く。**
type Fetched struct {
	// Source は取得元（store.Source*）。
	Source string
	// SourceID は取得元での ID。読売は棋譜 ID、連盟は中継のパス。
	SourceID string
	// SourceURL は諸元（どこから取ったか）。保存時に Save が決め直す。
	SourceURL string

	Event    string
	Handicap string
	Place    string
	Black    string
	White    string
	// StartedAt は開始日時（ゼロ値可）。
	StartedAt time.Time
	// EndMark は終局の種別（例 "投了"）。対局中は空。
	EndMark string
	// Moves は手数。**0 は「対局前」であってエラーではない。**
	Moves int

	// KIF は**登録された形式の原本**。整形し直さない。
	KIF string
	// Format は KIF の形式（空なら kif）。
	Format string
	// Encoding は元の文字コード（本文は UTF-8 に寄せてある。空なら utf-8）。
	Encoding string
}

// Finished は終局済みかどうかを返す。UI で手数の横に「（終局）」を出すのに使う。
func (f Fetched) Finished() bool { return f.EndMark != "" }

// Empty は取得結果が空（本文が無い）かどうかを返す。
func (f Fetched) Empty() bool { return strings.TrimSpace(f.KIF) == "" }

// fromYomiuri は読売のペイロード由来の棋譜を Fetched にする。
//
// メタデータは KIF を読み直さずペイロードの値を使う（そちらが一次情報）。
func fromYomiuri(g *scrape.Game) Fetched {
	return Fetched{
		Source:   store.SourceYomiuri,
		SourceID: g.SourceID,
		// 諸元: どこから取ったか。読売は棋譜ビューアの URL。
		SourceURL: scrape.ViewerURL(g.SourceID),
		Event:     g.Event,
		Handicap:  g.Handicap,
		Place:     g.Place,
		Black:     g.Black,
		White:     g.White,
		StartedAt: g.StartedAt,
		EndMark:   g.EndMark,
		Moves:     len(g.Moves),

		// スクレイピングは構造化データから組み立てるので、これ自体が原本。
		KIF:      g.KIF(),
		Format:   string(format.KIF),
		Encoding: EncodingUTF8,
	}
}

// fromShogiLive は連盟の中継の .kif を Fetched にする。
//
// 本文は**サイトが配信している原本のまま**運ぶ（整形し直さない）。
// 画面に出すメタデータだけ解析結果から取る。
func (f *Fetcher) fromShogiLive(g *scrape.LiveKifu) Fetched {
	got := fromDocument(g.Doc, store.SourceShogiLive, "", g.KIF, g.Encoding)
	got.SourceID = g.SourceID
	// 諸元: どこから取ったか。人が開いて確認するのは .kif ではなく中継ページ。
	got.SourceURL = f.shogilive.ViewerURL(g.SourceID)
	return got
}

// fromShogiDB2 は将棋DB2 の棋譜を Fetched にする。
//
// 読売と同じく**構造化データから組み立てた KIF が原本**（サイトの KIF は
// 消費時間を 0:00 で埋めた作り物なので使っていない）。メタデータもデータの値を使う。
func (f *Fetcher) fromShogiDB2(g *scrape.ShogiDB2Game) Fetched {
	return Fetched{
		Source:   store.SourceShogiDB2,
		SourceID: g.SourceID,
		// 諸元: どこから取ったか。対局ページの URL。
		SourceURL: f.shogidb2.GameURL(g.SourceID),
		Event:     g.Event,
		Handicap:  g.Handicap,
		Place:     g.Place,
		Black:     g.Black,
		White:     g.White,
		StartedAt: g.StartedAt,
		EndMark:   g.EndMark,
		Moves:     len(g.Moves),

		KIF:      g.KIF(),
		Format:   string(format.KIF),
		Encoding: EncodingUTF8,
	}
}

// fromDocument は解析結果を Fetched にする。
//
// **KIF は解析結果を組み立て直したものではなく原本（body）をそのまま入れる。**
// 保存されるのも原本なので、画面と保存内容を食い違わせないため
// （組み立て直すと変化・コメント・不成などが落ちる）。
func fromDocument(doc kifu.Document, source, sourceURL, body, encoding string) Fetched {
	if encoding == "" {
		encoding = EncodingUTF8
	}
	return Fetched{
		Source:    source,
		SourceURL: sourceURL,
		Event:     doc.Event,
		Handicap:  doc.Handicap,
		Place:     doc.Place,
		Black:     doc.Black,
		White:     doc.White,
		StartedAt: doc.StartedAt,
		EndMark:   doc.EndMark(),
		Moves:     len(doc.Moves),

		KIF:      body,
		Format:   string(format.KIF),
		Encoding: encoding,
	}
}

// countMoves は KIF テキストの手数を数える。
//
// **行数を数えるのではなく core/kifu で解析する。** 連盟が配信する .kif は
// `# --- Kifu for Windows ...` に始まり、指し手の間に `*` の観戦記コメントが
// 大量に入る。行を数えるとそれらまで手数に入る。
func countMoves(kifText string) int {
	doc, err := kifu.Parse(kifText)
	if err != nil {
		return 0
	}
	return len(doc.Moves)
}

// RefetchableURL は「その取得元 URL をもう一度取りに行けば同じ棋譜が取れるか」
// を判定し、取れるならその URL を、取れないなら空文字を返す。
//
// ⚠️ **この判断は kicho が持つ。呼び出し側に書かないこと。** 取り直せない URL を
// 「再読み込み」の口として画面に出すと、押すと必ず失敗するボタンになる。
//
//	shogilive … 中継ページ(HTML)。`Fetch` が棋譜 ID に解決して取り直せる
//	yomiuri   … 棋譜ビューアの URL。`Fetch` が ID を取り出して取り直せる
//	shogidb2  … 対局ページの URL。`Fetch` が ID を取り出して取り直せる
//	url       … 指定された .kif そのもの。取り直せる
//	paste     … ⚠️ **取り直せない。** 取得元が無い(空のまま)
//
// ⚠️ **読売を「取り直せない」にしないこと。** 読売は .kif を置いていないが、
// 判定は「その URL を .kif として読めるか」ではなく**「`Fetch` にその URL を
// 渡せば同じ棋譜が取れるか」**。`Fetch` は yomiuri.co.jp の URL を
// 棋譜 ID に解決してペイロードから組み立て直すので、**ビューアの URL を
// 渡せば取り直せる。**
//
// これが効くのは**利用側の「再読み込み」**（ikkyoku の解析タブ）。あちらは
// **食い違ったところから先だけを差し替えて検討の枝と評価値を残す**ので、
// ここで空を返すと「中継を追いながら検討する」流れでは
// **カードの更新 → 解析（根ごと入れ替え＝評価値が全部消える）しか手が無くなる。**
func RefetchableURL(source, sourceURL string) string {
	if source == store.SourcePaste {
		return ""
	}
	return sourceURL
}
