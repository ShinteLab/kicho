package kicho

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/kicho/format"
	"github.com/ShinteLab/kicho/store"
)

// maxKifuBytes は取り込む KIF の上限。棋譜1局は大きくても数十 KB なので、
// 誤って巨大なファイルや HTML を掴んだときに落とすための保険。
const maxKifuBytes = 4 << 20 // 4MiB

// 文字コードの記録に使う名前。
const (
	EncodingUTF8     = "utf-8"
	EncodingShiftJIS = "shift_jis"
)

// DecodeKIF は棋譜のバイト列を UTF-8 文字列にし、元の文字コード名を返す。
//
// 世に出回っている .kif は **Shift_JIS が多い**（Kifu for Windows 等の既定）。
// UTF-8 として妥当ならそのまま、そうでなければ Shift_JIS として解釈する。
//
// 保存する本文は UTF-8 に寄せる。元の文字コードは記録だけ残す（主にデバッグ用）。
func DecodeKIF(b []byte) (text, encoding string, err error) {
	if utf8.Valid(b) {
		return string(b), EncodingUTF8, nil
	}
	out, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), b)
	if err != nil {
		return "", "", fmt.Errorf("文字コードを判別できませんでした（UTF-8 でも Shift_JIS でもありません）: %w", err)
	}
	return string(out), EncodingShiftJIS, nil
}

// ImportKIF は KIF テキストを解析して保存する。
//
// 取り込みは毎回新しい棋譜として登録する（同じものを貼り直せば2件になる）。
// スクレイピング経由と違い、取得元での一意な ID が無いため。
func (l *Library) ImportKIF(ctx context.Context, text string) (store.Record, error) {
	return l.importDocument(ctx, text, store.SourcePaste, "", EncodingUTF8)
}

// ImportURL は URL から KIF を取得して保存する。
//
// スクレイピングではなく、URL の中身をそのまま KIF として読む。
// 他サイトの .kif ファイルや、別の kicho の /kifu/{id} を取り込める。
// 棋譜中継ページ(HTML)の URL を渡した場合はそこから .kif を辿る。
func (l *Library) ImportURL(ctx context.Context, rawURL string) (store.Record, error) {
	text, encoding, err := l.FetchKIFFromURL(ctx, rawURL)
	if err != nil {
		return store.Record{}, err
	}
	return l.importDocument(ctx, text, store.SourceURL, rawURL, encoding)
}

// FetchKIFFromURL は URL から棋譜テキストを取得する（保存はしない）。
// 本文は UTF-8 に寄せ、元の文字コード名も返す。
//
// .kif が返ってくればそのまま読む。HTML（棋譜中継ページ）が返ってきた場合だけ、
// そこに書かれている .kif の URL を辿って取り直す（`importer_html.go`）。
// 中継ページの URL をそのまま貼れるようにするためで、
// **ページから対局内容を読み取っているわけではない**（取り込むのは .kif の原本）。
func (l *Library) FetchKIFFromURL(ctx context.Context, rawURL string) (text, encoding string, err error) {
	u, err := parseHTTPURL(rawURL)
	if err != nil {
		return "", "", err
	}

	b, err := fetchLimited(ctx, u)
	if err != nil {
		return "", "", err
	}
	if looksLikeHTML(b) {
		kifURL, err := kifURLFromHTML(u, b)
		if err != nil {
			return "", "", err
		}
		if b, err = fetchLimited(ctx, kifURL); err != nil {
			return "", "", err
		}
	}
	return DecodeKIF(b)
}

// parseHTTPURL は入力を http/https の URL として読む。
func parseHTTPURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("URL の形式が不正です: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("http/https の URL を指定してください: %s", rawURL)
	}
	return u, nil
}

// fetchLimited は URL の中身を maxKifuBytes まで読む。
func fetchLimited(ctx context.Context, u *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("取得に失敗しました: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("取得に失敗しました（HTTP %d）: %s", resp.StatusCode, u)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxKifuBytes+1))
	if err != nil {
		return nil, fmt.Errorf("読み込みに失敗しました: %w", err)
	}
	if len(b) > maxKifuBytes {
		return nil, fmt.Errorf("内容が大きすぎます（%d バイト超）", maxKifuBytes)
	}
	return b, nil
}

// ParseKIF は KIF テキストを解析する（保存はしない。取り込み前の確認用）。
func ParseKIF(text string) (kifu.Document, error) {
	return kifu.Parse(text)
}

// importDocument は棋譜テキストを解析してメタデータを取り出し、
// **本文は原本のまま**保存する。
//
// 解析結果（棋戦名・対局者・日付・手数・終局）は一覧と検索のために使うだけで、
// 本文は整形し直さない。整形すると変化・コメント・不成などの情報が落ちるため。
func (l *Library) importDocument(ctx context.Context, text, source, sourceURL, encoding string) (store.Record, error) {
	if strings.TrimSpace(text) == "" {
		return store.Record{}, fmt.Errorf("棋譜が空です")
	}
	doc, err := kifu.Parse(text)
	if err != nil {
		return store.Record{}, err
	}

	return l.store.Save(ctx, store.Game{
		Source: source,
		// 取得元での一意な ID が無いので毎回新しく振る（登録のたびに別の棋譜になる）。
		SourceID:  uuid.NewString(),
		SourceURL: sourceURL,
		Event:     doc.Event,
		Handicap:  doc.Handicap,
		Place:     doc.Place,
		Black:     doc.Black,
		White:     doc.White,
		StartedAt: doc.StartedAt,
		EndMark:   doc.EndMark(),
		Moves:     len(doc.Moves),

		Body:     text, // 原本そのまま
		Format:   string(format.KIF),
		Encoding: encoding,
	})
}
