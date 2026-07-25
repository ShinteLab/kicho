// Package format は棋譜フォーマットの種別と、形式間の変換を扱う。
//
// kicho は**登録された形式のまま原本を保存する**。変換は要求されたときに行い、
// 変換結果は保存しない(形式ごとに持つと件数 × 形式で膨らむうえ、
// 変換は決定的なので都度計算で足りる)。
//
// 現時点で実装している変換は無い。`/kifu/{id}.{ext}` で原本と同じ形式を
// 要求された場合はそのまま返し、それ以外は ErrUnsupported になる。
// 変換を足すときは Register で登録する。
package format

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Format は棋譜フォーマットの種別。値は拡張子としても使う。
type Format string

const (
	KIF  Format = "kif"  // KIF 形式(移動元座標つき)
	KI2  Format = "ki2"  // KI2 形式(移動元を書かず 右/左/上/引/寄/直 で区別)
	CSA  Format = "csa"  // CSA 形式
	USI  Format = "usi"  // USI の position コマンド列
	SFEN Format = "sfen" // SFEN(局面)
	JKF  Format = "jkf"  // JSON Kifu Format
	USEN Format = "usen" // ShogiHome の圧縮形式
)

// All は扱いを想定している形式(ShogiHome が Web 棋譜として挙げているもの)。
var All = []Format{KIF, KI2, CSA, USI, SFEN, JKF, USEN}

// ErrUnsupported は要求された変換が未実装であることを表す。
var ErrUnsupported = errors.New("kicho: この形式への変換は未対応です")

// Default は形式が記録されていない場合の既定(v3 より前に保存したもの)。
const Default = KIF

// Parse は文字列(拡張子)を Format にする。大文字小文字と先頭の "." は無視する。
func Parse(s string) (Format, bool) {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
	for _, f := range All {
		if string(f) == s {
			return f, true
		}
	}
	return "", false
}

// Ext は拡張子(先頭の "." は含まない)を返す。
func (f Format) Ext() string { return string(f) }

// ContentType は配信時の Content-Type を返す。
func (f Format) ContentType() string {
	switch f {
	case JKF:
		return "application/json; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

// ConvertFunc は形式変換。
type ConvertFunc func(body string) (string, error)

// registry は (変換元, 変換先) → 変換関数。
var registry = map[[2]Format]ConvertFunc{}

// Register は変換を登録する。同じ組を二重登録した場合は panic する
// (初期化時の設定ミスを起動時に気付けるように)。
func Register(from, to Format, fn ConvertFunc) {
	key := [2]Format{from, to}
	if _, dup := registry[key]; dup {
		panic(fmt.Sprintf("format: %s -> %s の変換が二重登録されています", from, to))
	}
	registry[key] = fn
}

// CanConvert は変換できるかを返す(同じ形式なら常に true)。
func CanConvert(from, to Format) bool {
	if from == to {
		return true
	}
	_, ok := registry[[2]Format{from, to}]
	return ok
}

// Convert は body を from から to へ変換する。
// 同じ形式なら何もせずそのまま返す。未実装なら ErrUnsupported。
func Convert(body string, from, to Format) (string, error) {
	if from == to {
		return body, nil
	}
	fn, ok := registry[[2]Format{from, to}]
	if !ok {
		return "", fmt.Errorf("%w: %s → %s", ErrUnsupported, from, to)
	}
	return fn(body)
}

// SupportedTargets は from から変換できる形式を返す(自分自身を含む)。
func SupportedTargets(from Format) []Format {
	out := []Format{from}
	for key := range registry {
		if key[0] == from {
			out = append(out, key[1])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
