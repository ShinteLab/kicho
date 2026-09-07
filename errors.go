package kicho

import "errors"

// 取り込み・取得で返る sentinel。
//
// ⚠️ **文言比較で分岐させないために置いてある。** kicho は ikkyoku から
// ライブラリとして使われており、画面と文言はあちら側にある。呼び出し側が
// 「棋譜が空」「そもそも KIF ではない」「扱えない取得元」を見分けられないと、
// エラーの出し分けが `strings.Contains` になってしまう。
//
// 判定は errors.Is で行う（メッセージには入力の内容を足して包んである）。
var (
	// ErrNoInput は URL / 棋譜 ID が空。
	ErrNoInput = errors.New("URL または棋譜 ID を入力してください")
	// ErrEmptyKifu は本文が空（取得も貼り付けも中身が無い）。
	ErrEmptyKifu = errors.New("棋譜が空です")
	// ErrNotKifu は KIF として読み取れなかった（HTML やただの文章）。
	//
	// **手数 0 はこれに当たらない。** 中継は対局開始前からヘッダだけの .kif を
	// 置いており、1 手も無いのは正当な状態（`kifu.Document.Empty()` で判定する）。
	ErrNotKifu = errors.New("KIF として読み取れませんでした")
	// ErrUnsupportedSource は取得元が store.Source* のどれでもない。
	ErrUnsupportedSource = errors.New("扱えない取得元です")
)
