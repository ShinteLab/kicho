package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ShinteLab/core/kifu"
)

// ShogiLive は日本将棋連盟の棋譜中継(live.shogi.or.jp)を扱う。
//
// **読売と違ってスクレイピングではない。** 連盟は中継ページの隣に .kif を
// そのまま置いているので、取りに行くのはその .kif で、中身は原本のまま扱う
// (整形し直さない)。HTML を読むのは「どの .kif か」を決めるときだけ。
//
// それでも登録タブ(URL 取り込み)ではなく取得タブ側に置いてある。
// 中継は**対局中に随時更新される**ため、カードとして積んで「更新」で
// 取り直し、終局後に保存する、という読売と同じ扱いが要るため。
type ShogiLive struct {
	Client *http.Client
	// BaseURL は取得元(テストで差し替える)。既定は ShogiLiveBaseURL。
	BaseURL string
}

// ShogiLiveBaseURL は棋譜中継の基点。
//
// **https ではつながらない**(2026-07 時点で TLS の待ち受けが無い)。
// http のままにしておくこと。
const ShogiLiveBaseURL = "http://live.shogi.or.jp"

// NewShogiLive は既定の HTTP クライアントを持つ ShogiLive を返す。
func NewShogiLive() *ShogiLive {
	return &ShogiLive{Client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *ShogiLive) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *ShogiLive) base() string {
	if s.BaseURL != "" {
		return strings.TrimSuffix(s.BaseURL, "/")
	}
	return ShogiLiveBaseURL
}

// LiveKifu は中継から取得した1局分。
//
// KIF は**サイトが配信している原本**(文字コードだけ UTF-8 に寄せたもの)。
// Doc は一覧・検索に使うメタデータを取るために解析した結果で、
// 保存するのはあくまで KIF のほう。
type LiveKifu struct {
	SourceID string // 中継のパス(拡張子なし)。例 "oui/kifu/67/oui202607290101"
	KIF      string
	Encoding string // 元の文字コード(連盟は Shift_JIS)
	Doc      kifu.Document
}

// Finished は終局しているかどうかを返す(対局中なら false)。
func (l *LiveKifu) Finished() bool { return l.Doc.EndMark() != "" }

// KifuURL は棋譜 ID から .kif の URL を組み立てる。
func (s *ShogiLive) KifuURL(id string) string { return s.base() + "/" + id + ".kif" }

// ViewerURL は棋譜 ID から中継ページ(HTML)の URL を組み立てる。
// 保存した棋譜の諸元(どこから取ったか)として残す。人が開いて確認するのはこちら。
func (s *ShogiLive) ViewerURL(id string) string { return s.base() + "/" + id + ".html" }

// IsShogiLiveURL は live.shogi.or.jp の URL かどうかを返す。
// 取得タブの入力をどちらの取得元に回すかの判定に使う。
func IsShogiLiveURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "live.shogi.or.jp")
}

// ResolveID は入力を中継の棋譜 ID(拡張子なしのパス)に解決する。
//
// 受け付けるのは次の3つ。
//
//   - 中継ページの URL      .../oui/kifu/67/oui202607290101.html
//   - .kif の URL           .../oui/kifu/67/oui202607290101.kif
//   - 棋譜 ID そのもの      oui/kifu/67/oui202607290101
//
// 中継ページを渡された場合だけ、ページが読んでいる .kif を辿って決める
// (拡張子の付け替えでは決めない。何を表示しているかはページが持つ情報)。
func (s *ShogiLive) ResolveID(ctx context.Context, input string) (string, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return "", fmt.Errorf("URL または棋譜 ID を入力してください")
	}
	if !strings.HasPrefix(in, "http://") && !strings.HasPrefix(in, "https://") {
		return strings.Trim(in, "/"), nil // 棋譜 ID とみなす
	}

	u, err := url.Parse(in)
	if err != nil {
		return "", fmt.Errorf("URL の形式が不正です: %w", err)
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".kif") {
		return kifuIDFromPath(u.Path)
	}

	// 中継ページ。KIF_FILE_NAME を見てどの .kif かを決める。
	page, err := getBytes(ctx, s.client(), u.String())
	if err != nil {
		return "", err
	}
	kifURL, err := KifURLFromHTML(u, page)
	if err != nil {
		return "", err
	}
	return kifuIDFromPath(kifURL.Path)
}

// kifuIDFromPath は "/oui/kifu/67/oui202607290101.kif" を
// "oui/kifu/67/oui202607290101" にする。
//
// パスをそのまま ID にしているのは、中継が S3 のディレクトリ構成そのままで
// 一覧も検索も無いため。ID から .kif / .html の URL を組み立て直せる形が要る。
func kifuIDFromPath(p string) (string, error) {
	id := strings.Trim(p, "/")
	id = strings.TrimSuffix(id, ".kif")
	if id == "" {
		return "", fmt.Errorf("棋譜 ID を取り出せませんでした: %q", p)
	}
	return id, nil
}

// FetchGame は棋譜 ID から1局分を取得する。
//
// 対局中は .kif が伸びていくので、そのつど取りに行く前提(キャッシュしない)。
func (s *ShogiLive) FetchGame(ctx context.Context, id string) (*LiveKifu, error) {
	id = strings.Trim(strings.TrimSpace(id), "/")
	if id == "" {
		return nil, fmt.Errorf("scrape: empty shogilive kifu id")
	}
	b, err := getBytes(ctx, s.client(), s.KifuURL(id))
	if err != nil {
		return nil, err
	}
	if LooksLikeHTML(b) {
		// S3 の 404 ページなどを掴んだ場合。KIF として解析すると
		// 分かりにくいエラーになるのでここで止める。
		return nil, fmt.Errorf("棋譜(.kif)ではなく HTML が返りました: %s", s.KifuURL(id))
	}

	text, encoding, err := DecodeKIF(b)
	if err != nil {
		return nil, err
	}
	doc, err := kifu.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("shogilive %s: %w", id, err)
	}
	return &LiveKifu{SourceID: id, KIF: text, Encoding: encoding, Doc: doc}, nil
}

// FetchKifu は棋譜 ID から KIF 本文だけを返す(HTTP のライブ経路用)。
func (s *ShogiLive) FetchKifu(ctx context.Context, id string) (string, error) {
	g, err := s.FetchGame(ctx, id)
	if err != nil {
		return "", err
	}
	return g.KIF, nil
}
