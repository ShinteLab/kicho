// Package httpapi は棋譜を配信する HTTP エンドポイントを提供する。
//
// ShogiHome 等の外部ツールが「URL から棋譜を取得する」機能で使うことを想定している。
// Wails には依存しない(起動・停止はデスクトップ側の Service から呼ぶ)。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/kicho/format"
	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// LoopbackHost は既定の bind アドレス(この端末からのみアクセス可能)。
const LoopbackHost = "127.0.0.1"

// AllInterfacesHost は LAN 公開用の bind アドレス。
const AllInterfacesHost = "0.0.0.0"

// DefaultPort は既定のポート(Node 版の kicho.js と同じ)。
const DefaultPort = 3000

// Config は待ち受け設定。
type Config struct {
	Host string // bind アドレス(空なら LoopbackHost)
	Port int    // ポート。net.Listen と同じく 0 は OS による自動割り当て
}

// DefaultConfig は既定の待ち受け設定(localhost:3000)を返す。
func DefaultConfig() Config {
	return Config{Host: LoopbackHost, Port: DefaultPort}
}

// IsPublic は LAN(ループバック以外)に公開する設定かどうかを返す。
// UI で警告を出すために使う。
func (c Config) IsPublic() bool {
	host := c.Host
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	return !strings.EqualFold(host, "localhost")
}

func (c Config) addr() string {
	host := c.Host
	if host == "" {
		host = LoopbackHost
	}
	return net.JoinHostPort(host, fmt.Sprint(c.Port))
}

// Fetcher は読売の取得元(テストで差し替えられるようにインターフェースにしている)。
type Fetcher interface {
	FetchGame(ctx context.Context, id string) (*scrape.Game, error)
}

// LiveFetcher は KIF をそのまま配信しているサイトの取得元。
// 連盟の中継は構造化データではなく .kif を返すので、KIF 本文だけを受け取る。
type LiveFetcher interface {
	FetchKifu(ctx context.Context, id string) (string, error)
}

// Server は棋譜配信サーバ。起動・停止を繰り返せる。
type Server struct {
	store     *store.Store
	fetcher   Fetcher
	shogilive LiveFetcher
	shogidb2  LiveFetcher
	logger    *slog.Logger

	mu   sync.Mutex
	srv  *http.Server
	addr string // 実際に listen しているアドレス(停止中は "")
}

// New はサーバを作る(この時点では listen しない)。
func New(st *store.Store, f Fetcher, live LiveFetcher, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{store: st, fetcher: f, shogilive: live, logger: logger}
}

// WithShogiDB2 は将棋DB2 の取得元を付ける(付けなければ /shogidb2/kifu/ は 501)。
// Start より前に呼ぶこと。
func (s *Server) WithShogiDB2(f LiveFetcher) *Server {
	s.shogidb2 = f
	return s
}

// Start は待ち受けを開始する。既に起動している場合はエラー。
//
// ポートが使えない等の失敗は Start の時点で返す(起動できたかどうかを
// UI に即座に返せるよう、listen まで同期で行う)。
func (s *Server) Start(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.srv != nil {
		return fmt.Errorf("kicho: server is already running on %s", s.addr)
	}

	ln, err := net.Listen("tcp", cfg.addr())
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.addr(), err)
	}

	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.srv = srv
	s.addr = ln.Addr().String()

	if cfg.IsPublic() {
		s.logger.Warn("kicho HTTP server is exposed beyond localhost",
			"addr", s.addr)
	}
	s.logger.Info("kicho HTTP server started", "addr", s.addr)

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("kicho HTTP server stopped unexpectedly", "error", err)
		}
	}()
	return nil
}

// Stop は待ち受けを停止する。起動していなければ何もしない。
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	addr := s.addr
	s.addr = ""
	s.mu.Unlock()

	if srv == nil {
		return nil
	}
	s.logger.Info("kicho HTTP server stopping", "addr", addr)
	return srv.Shutdown(ctx)
}

// Running は待ち受け中かどうかと、その実アドレスを返す。
func (s *Server) Running() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil, s.addr
}

// パス組み立て。外部ツールに渡す URL はここで作る(フロントに直書きしない)。
// routes() の登録パターンと対応しており、server_test.go で実際に到達できるか検証している。

// KifuPath は保存済み棋譜のパスを返す(kicho 自前の ID)。
func KifuPath(id string) string { return "/kifu/" + url.PathEscape(id) }

// RyuohKifuPath は読売から直接取得するパスを返す(取得元の棋譜 ID)。
// 対局中の棋譜はこちらを使う(毎回サイトへ取りに行く)。
func RyuohKifuPath(sourceID string) string {
	return "/ryuoh/kifu/" + url.PathEscape(sourceID)
}

// ShogiLiveKifuPath は日本将棋連盟の中継から直接取得するパスを返す。
//
// 連盟の棋譜 ID は中継のパスそのもの("oui/kifu/67/oui202607290101")なので
// スラッシュを含む。ルート側を `{id...}` にしてあるため、
// **スラッシュは区切りとして残し、各セグメントだけをエスケープする**。
func ShogiLiveKifuPath(sourceID string) string {
	parts := strings.Split(strings.Trim(sourceID, "/"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "/shogilive/kifu/" + strings.Join(parts, "/")
}

// ShogiDB2KifuPath は将棋DB2 から直接取得するパスを返す(対局ページの /games/{id} の id)。
func ShogiDB2KifuPath(sourceID string) string {
	return "/shogidb2/kifu/" + url.PathEscape(sourceID)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// 保存済み棋譜(kicho 自前の ID)
	mux.HandleFunc("GET /kifu", s.handleList)
	mux.HandleFunc("GET /kifu/{id}", s.handleSavedKifu)

	// 読売から直取得(Node 版 kicho.js との互換エンドポイント)
	mux.HandleFunc("GET /ryuoh/kifu/{id}", s.handleRyuohKifu)

	// 日本将棋連盟の中継から直取得。棋譜 ID がパス("oui/kifu/67/...")なので
	// スラッシュを含む。`{id...}` で残り全部を受ける。
	mux.HandleFunc("GET /shogilive/kifu/{id...}", s.handleLiveKifu("shogilive", func() LiveFetcher { return s.shogilive }))

	// 将棋DB2 から直取得。サイトは KIF を配っていないので kicho が組み立てたものを返す。
	mux.HandleFunc("GET /shogidb2/kifu/{id}", s.handleLiveKifu("shogidb2", func() LiveFetcher { return s.shogidb2 }))

	mux.HandleFunc("GET /", s.handleIndex)
	return mux
}

const kifuContentType = "text/plain; charset=utf-8"

// writeBody は棋譜本文を指定形式として返す。
func writeBody(w http.ResponseWriter, f format.Format, filename, body string) {
	w.Header().Set("Content-Type", f.ContentType())
	if filename != "" {
		// ShogiHome 等が保存するときのファイル名のヒント。
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`inline; filename*=UTF-8''%s`, urlEscape(filename)))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// writeKIF はライブ取得(常に KIF)用。
func writeKIF(w http.ResponseWriter, filename, kif string) {
	writeBody(w, format.KIF, filename, kif)
}

func writeStatus(w http.ResponseWriter, status int, contentType, msg string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, msg)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeStatus(w, status, kifuContentType, msg+"\n")
}

// GET /kifu/{id} — 保存済み棋譜を**登録された形式の原本のまま**返す。
// GET /kifu/{id}.{ext} — その形式に変換して返す。
//
// 変換結果は保存しない(変換は決定的なので都度計算で足りる)。
// 未実装の形式は 501 を返す。変換は kicho/format に登録して増やす。
func (s *Server) handleSavedKifu(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	id, want, hasExt := splitFormatExt(raw)

	rec, err := s.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "kifu not found")
		return
	}
	if err != nil {
		s.logger.Error("get saved kifu", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "kifu error")
		return
	}

	stored, ok := format.Parse(rec.Format)
	if !ok {
		stored = format.Default
	}
	// 拡張子が無ければ原本の形式のまま返す。
	if !hasExt {
		want = stored
	}

	body, err := format.Convert(rec.Body, stored, want)
	if errors.Is(err, format.ErrUnsupported) {
		writeStatus(w, http.StatusNotImplemented, want.ContentType(),
			fmt.Sprintf("%s から %s への変換は未対応です\n対応: %s\n",
				stored, want, joinFormats(format.SupportedTargets(stored))))
		return
	}
	if err != nil {
		s.logger.Error("convert kifu", "id", id, "from", stored, "to", want, "error", err)
		writeError(w, http.StatusInternalServerError, "kifu error")
		return
	}

	writeBody(w, want, kifFilename(rec, want), body)
}

// splitFormatExt は "{id}.{ext}" を分解する。
// 既知の形式の拡張子でなければ ID の一部として扱う(ID に "." は入らないが、念のため)。
func splitFormatExt(raw string) (id string, f format.Format, hasExt bool) {
	i := strings.LastIndex(raw, ".")
	if i <= 0 {
		return raw, format.Default, false
	}
	parsed, ok := format.Parse(raw[i+1:])
	if !ok {
		return raw, format.Default, false
	}
	return raw[:i], parsed, true
}

func joinFormats(fs []format.Format) string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Ext())
	}
	return strings.Join(out, ", ")
}

// GET /ryuoh/kifu/{id} — 読売から直接取得して KIF で返す(保存はしない)。
//
// リクエストのたびにサイトへ取りに行く(キャッシュしない)。対局中の棋譜は
// 随時更新されるため、外部ツール側やプロキシにもキャッシュさせない。
func (s *Server) handleRyuohKifu(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := s.fetcher.FetchGame(r.Context(), id)
	if err != nil {
		s.logger.Error("fetch ryuoh kifu", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "kifu error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeKIF(w, "", g.KIF())
}

// GET /shogilive/kifu/{id...} ・ GET /shogidb2/kifu/{id} — 取得元から直接取得して
// KIF で返す(保存はしない)。
//
// 連盟は Shift_JIS で配信しているので、ここで **UTF-8 に寄せて**返す。
// 読売と同じくリクエストのたびに取りに行く(キャッシュしない)。
func (s *Server) handleLiveKifu(name string, fetcher func() LiveFetcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f := fetcher()
		if f == nil {
			writeError(w, http.StatusNotImplemented, name+" fetcher is not configured")
			return
		}
		kif, err := f.FetchKifu(r.Context(), id)
		if err != nil {
			s.logger.Error("fetch "+name+" kifu", "id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "kifu error")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeKIF(w, "", kif)
	}
}

// GET /kifu — 保存済み棋譜の一覧を JSON で返す。
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSummary(r.Context())
	if err != nil {
		s.logger.Error("list kifu", "error", err)
		writeError(w, http.StatusInternalServerError, "kifu error")
		return
	}

	type item struct {
		ID        string `json:"id"`
		Event     string `json:"event"`
		Black     string `json:"black"`
		White     string `json:"white"`
		StartedAt string `json:"startedAt,omitempty"`
		URL       string `json:"url"`
	}
	out := make([]item, 0, len(list))
	for _, rec := range list {
		it := item{
			ID:    rec.ID,
			Event: rec.Event,
			Black: rec.Black,
			White: rec.White,
			URL:   KifuPath(rec.ID),
		}
		if !rec.StartedAt.IsZero() {
			it.StartedAt = rec.StartedAt.Format(time.RFC3339)
		}
		out = append(out, it)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		s.logger.Error("encode kifu list", "error", err)
	}
}

// GET / — 何が使えるかの案内(外部ツールに URL を貼るときの確認用)。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", kifuContentType)
	_, _ = fmt.Fprint(w, strings.Join([]string{
		"kicho (棋帳)",
		"",
		"GET /kifu                保存済み棋譜の一覧 (JSON)",
		"GET /kifu/{id}           保存済み棋譜 (KIF)",
		"GET /ryuoh/kifu/{id}     読売(竜王戦)から直接取得 (KIF)",
		"GET /shogilive/kifu/{id} 日本将棋連盟の中継から直接取得 (KIF)",
		"GET /shogidb2/kifu/{id}  将棋DB2 から直接取得 (KIF)",
		"",
	}, "\n"))
}

// urlEscape は Content-Disposition の filename*(RFC 5987)用にエスケープする。
func urlEscape(s string) string {
	return url.PathEscape(s)
}

// kifFilename は保存済み棋譜のダウンロード名を組み立てる。
func kifFilename(rec store.Record, f format.Format) string {
	parts := make([]string, 0, 3)
	if !rec.StartedAt.IsZero() {
		parts = append(parts, rec.StartedAt.Format("20060102"))
	}
	if rec.Event != "" {
		parts = append(parts, rec.Event)
	}
	if len(parts) == 0 {
		parts = append(parts, rec.ID)
	}
	return strings.Join(parts, "_") + "." + f.Ext()
}
