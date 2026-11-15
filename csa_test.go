package kicho

import (
	"context"
	"testing"

	"github.com/ShinteLab/kicho/format"
	"github.com/ShinteLab/kicho/store"
)

// csaSample は 2 手 + 投了の CSA。
const csaSample = "V2.2\nN+先手\nN-後手\n$EVENT:CSA の棋戦\nPI\n+\n+7776FU\n-3334FU\n%TORYO\n"

// **CSA も受け、本文は CSA のまま・形式は csa で持つ**（2026-11-15）。
// KIF に組み直すと原本が残らない（配るときに変換すればよい）。
func TestImportCSAKeepsOriginal(t *testing.T) {
	lib := newTestLibrary(t)
	rec, err := lib.ImportKIF(context.Background(), csaSample)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	if rec.Body != csaSample {
		t.Errorf("本文が原本のままでない:\n%s", rec.Body)
	}
	if rec.Format != string(format.CSA) {
		t.Errorf("Format = %q, want csa", rec.Format)
	}
	if rec.Event != "CSA の棋戦" || rec.Black != "先手" || rec.White != "後手" {
		t.Errorf("一覧の値が読めていない: %+v", rec)
	}
	if rec.Moves != 3 || rec.EndMark != "投了" {
		t.Errorf("手数 / 終局 = %d / %q, want 3 / 投了", rec.Moves, rec.EndMark)
	}
}

// 取り込み前の確認（貼り付け・URL と同じ判定）でも形式が付くこと。
// ⚠️ 保存（`Save`）は Fetched の形式をそのまま使うので、ここで落とすと KIF として保存される。
func TestPreviewCSA(t *testing.T) {
	got, err := NewFetcher().PreviewKIF(csaSample)
	if err != nil {
		t.Fatalf("PreviewKIF: %v", err)
	}
	if got.Format != string(format.CSA) || got.KIF != csaSample || got.Moves != 3 {
		t.Errorf("Fetched = %+v", got)
	}
	// 保存し直しても手数は本文（CSA）から数え直す。
	lib := newTestLibrary(t)
	got.Source, got.SourceID, got.Moves = store.SourceURL, "https://example.com/a.csa", 999
	rec, err := lib.Save(context.Background(), got)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if rec.Moves != 3 || rec.Format != string(format.CSA) {
		t.Errorf("Moves / Format = %d / %q", rec.Moves, rec.Format)
	}
}

// **形式は本文から決め直す**（2026-11-15）。画面を往復して形式の欄が落ちた・食い違った
// Fetched でも、CSA の本文は csa、KIF の本文は kif として記録すること
// （ikkyoku の中継カードの「保存」は形式を運んでいなかった）。
func TestSaveDerivesFormatFromBody(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	for _, tt := range []struct {
		name, body, given string
		want              format.Format
	}{
		{"形式の欄が空の CSA", csaSample, "", format.CSA},
		{"kif と言ってくる CSA", csaSample, string(format.KIF), format.CSA},
		{"csa と言ってくる KIF", importSample, string(format.CSA), format.KIF},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := lib.Save(ctx, Fetched{
				Source: store.SourceURL, SourceID: "https://example.com/" + tt.name,
				KIF: tt.body, Format: tt.given,
			})
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			if rec.Format != string(tt.want) {
				t.Errorf("Format = %q, want %q", rec.Format, tt.want)
			}
		})
	}
}

// CSA の指し手が途中で読めなくても断らない（本文は原本のまま残る）。
func TestImportCSABrokenMoves(t *testing.T) {
	lib := newTestLibrary(t)
	broken := "PI\n+\n+7776FU\n+3334FU\n"
	rec, err := lib.ImportKIF(context.Background(), broken)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	if rec.Body != broken || rec.Moves != 1 {
		t.Errorf("Body / Moves = %q / %d", rec.Body, rec.Moves)
	}
}

// **ファイルから読んだ棋譜は元の文字コードを記録する**（2026-11-15。`ImportKIFWithEncoding`）。
func TestImportKIFWithEncoding(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	rec, err := lib.ImportKIFWithEncoding(ctx, csaSample, EncodingShiftJIS)
	if err != nil {
		t.Fatalf("ImportKIFWithEncoding: %v", err)
	}
	if rec.Encoding != EncodingShiftJIS || rec.Format != string(format.CSA) || rec.Body != csaSample {
		t.Errorf("Encoding / Format = %q / %q", rec.Encoding, rec.Format)
	}
	// 知らない文字コードは黙って utf-8 にしない。
	if _, err := lib.ImportKIFWithEncoding(ctx, csaSample, "euc-jp"); err == nil {
		t.Error("知らない文字コードが通った")
	}
}
