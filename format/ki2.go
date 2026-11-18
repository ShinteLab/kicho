package format

import (
	"errors"
	"fmt"

	"github.com/ShinteLab/core/kifu"
)

func init() {
	Register(KI2, KIF, KI2ToKIF)
}

// KI2ToKIF は KI2 の原本を KIF にする（盤を進めながら移動元を割り出す。`kifu.KI2Document.KIF`）。
//
// ⚠️ **盤に当てられない手があってもエラーにしない。** そこまでの KIF を返し、止まった理由を
// 初期局面のコメントの先頭に書く（中継の誤記 1 つで配れなくなると、`/kifu/{id}.kif` も
// ライブ経路も使えなくなるため）。文面はこの 1 か所だけに置く（ライブ経路も同じものを使う）。
//
// エラーにするのは、別の局面として読めてしまうもの（知らない手合割・読めない盤面図）だけ。
func KI2ToKIF(body string) (string, error) {
	k, err := kifu.ParseKI2(body)
	if err != nil {
		return "", err
	}
	doc, errMove, err := k.KIF()
	if err != nil {
		if !errors.Is(err, kifu.ErrKI2Move) {
			return "", err
		}
		note := fmt.Sprintf("%d 手目「%s」を盤に当てられないため、%d 手目までです",
			errMove, moveText(k, errMove), errMove-1)
		if doc.Comment != "" {
			note += "\n" + doc.Comment
		}
		doc.Comment = note
	}
	return doc.String(), nil
}

// moveText は n 手目（1 始まり）の原本の表記を返す。
func moveText(k kifu.KI2Document, n int) string {
	if n < 1 || n > len(k.Moves) {
		return ""
	}
	return k.Moves[n-1].Text
}
