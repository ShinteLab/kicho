package scrape

import (
	"strings"
	"testing"
)

// 読売のペイロードと同じ「IIFE + 引数リストで値を共有する」形を最小構成で再現したもの。
// 値の重複排除(player1_image と player2_image が同じ変数 b を指す等)が
// 文字列パースでは解けないことを示すため、あえて変数参照を含めている。
const fixturePayload = `export default (function(a,b,c,d){return {data:{kifu:{
  play_id:"abc123",
  handicap:a,
  event:"テスト棋戦第１局",
  player1:"先手 太郎",
  player1_image:b,
  player2:"後手 次郎",
  player2_image:b,
  side:c,
  place:"テスト会館",
  starttime:"2024-10-19 09:00:00",
  end_mark:"投了",
  timelimit:480,
  countdown:60,
  kifu:[
    {num:0,move:d,fr_x:0,fr_y:0,spend:0},
    {num:1,move:"７六歩",fr_x:7,fr_y:7,spend:0},
    {num:2,move:"８四歩",fr_x:8,fr_y:3,spend:60},
    {num:3,move:"５五角打",fr_x:8,fr_y:8,spend:60},
    {num:4,move:"７八銀右",fr_x:6,fr_y:9,spend:120},
    {num:5,move:"投了打",fr_x:0,fr_y:0,spend:240}
  ]
}}}}("平手",null,"先手",null));`

func TestParsePayload(t *testing.T) {
	g, err := parsePayload(fixturePayload)
	if err != nil {
		t.Fatalf("parsePayload error: %v", err)
	}

	if g.SourceID != "abc123" {
		t.Errorf("SourceID = %q", g.SourceID)
	}
	if g.Event != "テスト棋戦第１局" {
		t.Errorf("Event = %q", g.Event)
	}
	// 引数リスト経由の値(a = "平手")が解決できていること。
	if g.Handicap != "平手" {
		t.Errorf("Handicap = %q, want 平手 (変数参照が解決できていない)", g.Handicap)
	}
	if g.Place != "テスト会館" {
		t.Errorf("Place = %q", g.Place)
	}

	if got := g.StartedAt.Format("2006-01-02 15:04:05"); got != "2024-10-19 09:00:00" {
		t.Errorf("StartedAt = %q", got)
	}
	if name, off := g.StartedAt.Zone(); name != "JST" || off != 9*60*60 {
		t.Errorf("StartedAt zone = %s %d, want JST 32400", name, off)
	}

	// 先頭要素(num=0, move=null)はメタ情報なので除外される。
	if len(g.Moves) != 5 {
		t.Fatalf("len(Moves) = %d, want 5", len(g.Moves))
	}
	if g.Moves[0].Num != 1 || g.Moves[0].Name != "７六歩" {
		t.Errorf("Moves[0] = %+v", g.Moves[0])
	}

	if g.EndMark != "投了" || !g.Finished() {
		t.Errorf("EndMark = %q, Finished = %v", g.EndMark, g.Finished())
	}
}

// 対局中(end_mark が無い)の棋譜は Finished() が false になること。
// 対局中は棋譜が随時更新されるので、UI で「終局」表示を出さない判断に使う。
func TestParsePayloadInProgress(t *testing.T) {
	src := strings.Replace(fixturePayload, `  end_mark:"投了",`, "", 1)
	g, err := parsePayload(src)
	if err != nil {
		t.Fatal(err)
	}
	if g.EndMark != "" || g.Finished() {
		t.Errorf("EndMark = %q, Finished = %v, want empty/false", g.EndMark, g.Finished())
	}
}

// side は player1 の手番。取り違えると保存する棋譜すべてで先後が反転するため、
// 両方向を固定する。
func TestParsePayloadSide(t *testing.T) {
	t.Run("side=先手 なら player1 が先手", func(t *testing.T) {
		g, err := parsePayload(fixturePayload)
		if err != nil {
			t.Fatal(err)
		}
		if g.Black != "先手 太郎" || g.White != "後手 次郎" {
			t.Errorf("Black=%q White=%q", g.Black, g.White)
		}
	})

	t.Run("side=後手 なら player2 が先手", func(t *testing.T) {
		src := strings.Replace(fixturePayload, `("平手",null,"先手",null)`, `("平手",null,"後手",null)`, 1)
		g, err := parsePayload(src)
		if err != nil {
			t.Fatal(err)
		}
		if g.Black != "後手 次郎" || g.White != "先手 太郎" {
			t.Errorf("Black=%q White=%q", g.Black, g.White)
		}
	})
}

func TestParsePayloadToKIF(t *testing.T) {
	g, err := parsePayload(fixturePayload)
	if err != nil {
		t.Fatal(err)
	}
	got := g.KIF()

	for _, want := range []string{
		"開始日時：2024/10/19 09:00:00\n",
		"棋戦：テスト棋戦第１局\n",
		"場所：テスト会館\n",
		"手合割：平手\n",
		"先手：先手 太郎\n",
		"後手：後手 次郎\n",
		"持ち時間：各8時間\n",
		"秒読み：60秒\n",
		"1 ７六歩(77)   ( 0:00/00:00:00)\n",
		"2 ８四歩(83)   ( 1:00/00:01:00)\n",
		"3 ５五角打(00)   ( 1:00/00:01:00)\n", // 打ちは移動元を 00 に。先手の累計
		"4 ７八銀(69)   ( 2:00/00:03:00)\n",  // 修飾"右"は除去。後手の累計
		"5 投了   ( 4:00/00:05:00)\n",       // 終局は座標なし・消費時間つき
	} {
		if !strings.Contains(got, want) {
			t.Errorf("KIF() missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestParsePayloadErrors(t *testing.T) {
	cases := map[string]string{
		"prefix なし":   `{"data":{}}`,
		"評価できない":      `export default (function({{{);`,
		"data が無い":    `export default ({});`,
		"kifu が無い":    `export default ({data:{}});`,
		"kifu が配列でない": `export default ({data:{kifu:{kifu:5}}});`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePayload(src); err == nil {
				t.Errorf("parsePayload(%q) succeeded, want error", src)
			}
		})
	}
}

// 対局前は kifu が空(または開始前のメタ要素のみ)で来る。
// **指し手が無いだけなのでエラーにしない。**
func TestParsePayloadNoMoves(t *testing.T) {
	cases := map[string]string{
		"指し手が空":    `export default ({data:{kifu:{kifu:[],event:"竜王戦",player1:"A",player2:"B"}}});`,
		"指し手がメタのみ": `export default ({data:{kifu:{kifu:[{num:0,move:null}],event:"竜王戦",player1:"A",player2:"B"}}});`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parsePayload(src)
			if err != nil {
				t.Fatal(err)
			}
			if len(g.Moves) != 0 {
				t.Errorf("len(Moves) = %d, want 0", len(g.Moves))
			}
			if g.Event != "竜王戦" || g.Black != "A" || g.White != "B" {
				t.Errorf("Event=%q Black=%q White=%q", g.Event, g.Black, g.White)
			}
			if g.Finished() {
				t.Error("Finished() = true, want false")
			}
		})
	}
}

// 第三者サイトのコードを評価するため、無限ループで固まらないこと。
func TestParsePayloadInterruptsInfiniteLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("evalTimeout 待ちのため -short では実行しない")
	}
	_, err := parsePayload(`export default (function(){while(true){}}());`)
	if err == nil {
		t.Fatal("infinite loop payload succeeded, want interrupt error")
	}
}
