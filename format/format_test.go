package format

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]Format{
		"kif":  KIF,
		"KIF":  KIF,
		".ki2": KI2,
		" csa": CSA,
		"jkf":  JKF,
	}
	for in, want := range cases {
		got, ok := Parse(in)
		if !ok || got != want {
			t.Errorf("Parse(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "txt", "pdf", "kifu"} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestConvertSameFormatIsPassthrough(t *testing.T) {
	body := "手合割：平手\n1 ７六歩(77)\n"
	got, err := Convert(body, KIF, KIF)
	if err != nil {
		t.Fatal(err)
	}
	if got != body {
		t.Error("同じ形式なら内容を変えないこと")
	}
	if !CanConvert(KIF, KIF) {
		t.Error("CanConvert(KIF, KIF) should be true")
	}
}

// 未実装の変換は ErrUnsupported になること(501 を返す判断に使う)。
func TestConvertUnsupported(t *testing.T) {
	_, err := Convert("...", KIF, KI2)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	// どの形式が要求されたか分かること。
	if !strings.Contains(err.Error(), "ki2") {
		t.Errorf("err = %v", err)
	}
	if CanConvert(KIF, KI2) {
		t.Error("CanConvert(KIF, KI2) should be false while unimplemented")
	}
}

// 変換を登録すれば使えるようになること(拡張点が機能すること)。
func TestRegisterAndConvert(t *testing.T) {
	const fake Format = "kif"
	// テスト内だけの登録。他のテストに影響しないよう後で消す。
	Register(fake, CSA, func(body string) (string, error) {
		return "converted:" + body, nil
	})
	defer delete(registry, [2]Format{fake, CSA})

	if !CanConvert(fake, CSA) {
		t.Fatal("登録した変換が見えていない")
	}
	got, err := Convert("body", fake, CSA)
	if err != nil {
		t.Fatal(err)
	}
	if got != "converted:body" {
		t.Errorf("= %q", got)
	}

	targets := SupportedTargets(fake)
	if len(targets) != 2 {
		t.Errorf("SupportedTargets = %v, want [csa kif]", targets)
	}
}

func TestContentType(t *testing.T) {
	if got := KIF.ContentType(); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("KIF ContentType = %q", got)
	}
	if got := JKF.ContentType(); !strings.HasPrefix(got, "application/json") {
		t.Errorf("JKF ContentType = %q", got)
	}
}
