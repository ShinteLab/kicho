package scrape

import (
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"

	"shinte/core/kifu"
)

// 読売のペイロードは Nuxt の `export default (function(a,b,...){...}(...))` 形式で、
// 値が引数リストの変数として共有(重複排除)されている。そのため文字列パースでは解けず、
// JS として評価する必要がある。ここでは goja(Pure Go の JS エンジン)で評価する。
const payloadPrefix = "export default "

// evalTimeout は取得したスクリプトの評価を打ち切るまでの時間。
// 第三者サイトのコードを実行するため、無限ループで固まらないよう必ず割り込む。
const evalTimeout = 10 * time.Second

// starttime のレイアウト(例 "2024-10-19 09:00:00")。タイムゾーン表記は無い。
const startTimeLayout = "2006-01-02 15:04:05"

// jst は starttime の解釈に使うタイムゾーン(日本の棋戦なので JST 固定)。
var jst = time.FixedZone("JST", 9*60*60)

// parsePayload は _payload.js の中身を評価して Game を組み立てる。
func parsePayload(src string) (g *Game, err error) {
	trimmed := strings.TrimSpace(src)
	if !strings.HasPrefix(trimmed, payloadPrefix) {
		return nil, fmt.Errorf("unexpected payload prefix: %.40q", trimmed)
	}
	expr := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(trimmed, payloadPrefix)), ";")

	vm := goja.New()

	// 評価が長引いたら割り込む(defer で必ず止める)。
	timer := time.AfterFunc(evalTimeout, func() {
		vm.Interrupt("payload evaluation timed out")
	})
	defer timer.Stop()

	// goja は null/undefined へのプロパティアクセスで panic するため、
	// 想定外の形をエラーに落とす。
	defer func() {
		if r := recover(); r != nil {
			g, err = nil, fmt.Errorf("unexpected payload shape: %v", r)
		}
	}()

	v, err := vm.RunString("(" + expr + ")")
	if err != nil {
		return nil, fmt.Errorf("evaluate payload: %w", err)
	}

	root := v.ToObject(vm)
	data := prop(vm, root, "data")
	if data == nil {
		return nil, fmt.Errorf("payload has no 'data'")
	}
	k := prop(vm, data, "kifu")
	if k == nil {
		return nil, fmt.Errorf("payload has no 'data.kifu'")
	}

	game := &Game{
		SourceID: str(vm, k, "play_id"),
		Event:    str(vm, k, "event"),
		Handicap: str(vm, k, "handicap"),
		Place:    str(vm, k, "place"),
		// 終局していれば "投了" 等が入る。対局中は空。
		EndMark: str(vm, k, "end_mark"),
		// timelimit は分、countdown は秒。
		TimeLimit: time.Duration(num(vm, k, "timelimit")) * time.Minute,
		Countdown: time.Duration(num(vm, k, "countdown")) * time.Second,
	}

	// side は player1 の手番。第37期竜王戦第1〜5局で side が先後交互になること、
	// および「残り時間が最初に減った手数の偶奇(奇数手＝先手)」と 5/5 一致することを
	// 実データで確認済み。ここを取り違えると保存する棋譜すべてで先後が反転する。
	p1, p2 := str(vm, k, "player1"), str(vm, k, "player2")
	if str(vm, k, "side") == "後手" {
		game.Black, game.White = p2, p1
	} else {
		game.Black, game.White = p1, p2
	}

	if s := str(vm, k, "starttime"); s != "" {
		if t, perr := time.ParseInLocation(startTimeLayout, s, jst); perr == nil {
			game.StartedAt = t
		}
	}

	moves := prop(vm, k, "kifu")
	if moves == nil {
		return nil, fmt.Errorf("payload has no 'data.kifu.kifu'")
	}
	n := moves.Get("length")
	if n == nil {
		return nil, fmt.Errorf("'data.kifu.kifu' is not an array")
	}
	count := n.ToInteger()
	for i := int64(0); i < count; i++ {
		el := moves.Get(fmt.Sprint(i))
		if el == nil || goja.IsUndefined(el) || goja.IsNull(el) {
			continue
		}
		o := el.ToObject(vm)
		// 先頭要素(num=0, move=null)は指し手ではなく開始前のメタ情報。
		name := str(vm, o, "move")
		if name == "" {
			continue
		}
		game.Moves = append(game.Moves, kifu.Move{
			Num:   int(num(vm, o, "num")),
			Name:  name,
			FromX: int(num(vm, o, "fr_x")),
			FromY: int(num(vm, o, "fr_y")),
			// spend はその手の消費時間(秒)。読売は分単位でしか記録していないため
			// 常に 60 の倍数になる。第37期竜王戦第1〜5局で、spend の合計が
			// 残り時間の減少分と先後とも一致することを確認済み。
			Spend: time.Duration(num(vm, o, "spend")) * time.Second,
		})
	}
	if len(game.Moves) == 0 {
		return nil, fmt.Errorf("payload contains no moves")
	}
	return game, nil
}

// prop はオブジェクトのプロパティをオブジェクトとして取り出す(無ければ nil)。
func prop(vm *goja.Runtime, o *goja.Object, key string) *goja.Object {
	v := o.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return v.ToObject(vm)
}

// str はプロパティを文字列として取り出す(無い・null なら "")。
func str(vm *goja.Runtime, o *goja.Object, key string) string {
	v := o.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// num はプロパティを整数として取り出す(無い・null なら 0)。
func num(vm *goja.Runtime, o *goja.Object, key string) int64 {
	v := o.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0
	}
	return v.ToInteger()
}
