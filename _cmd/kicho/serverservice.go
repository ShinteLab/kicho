package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/httpapi"
	"github.com/ShinteLab/kicho/settings"
	"github.com/ShinteLab/kicho/store"
)

// ServerService は棋譜配信 HTTP サーバの設定と起動/停止をフロントに公開する。
type ServerService struct {
	lib *kicho.Library
}

func NewServerService(lib *kicho.Library) *ServerService {
	return &ServerService{lib: lib}
}

// ServerStatus はサーバの現在状態。
type ServerStatus struct {
	Running bool `json:"running"`
	// Addr は実際に listen しているアドレス(停止中は空)。
	Addr string `json:"addr"`
	Host string `json:"host"`
	Port int    `json:"port"`
	// Public が true なら localhost 以外に公開されている(UI で警告を出す)。
	Public bool `json:"public"`
	// AutoStart はアプリ起動時に自動で listen するかどうか。
	AutoStart bool `json:"autoStart"`
	// URLs は外部ツールに貼れるベース URL の候補(末尾にスラッシュは付かない)。
	//
	// 停止中でも設定値から組み立てて返す。棋譜 URL のコピーは
	// サーバが動いていなくても行えた方が便利なため(貼り付け先で使う頃には
	// 起動しておけばよい)。LAN 公開時は実際に到達できる LAN アドレスも含む。
	URLs []string `json:"urls"`
}

// Status は現在の設定と稼働状態を返す。
func (s *ServerService) Status() (ServerStatus, error) {
	cfg, err := settings.Load()
	if err != nil {
		return ServerStatus{}, err
	}
	running, addr := s.lib.Server().Running()

	// 停止中は設定値のポートで組み立てる(稼働中は実際に listen しているポート。
	// ポート 0 を設定できない前提だが、念のため実アドレスを優先する)。
	port := cfg.Port
	if running {
		if _, p, err := net.SplitHostPort(addr); err == nil {
			if n, err := strconv.Atoi(p); err == nil {
				port = n
			}
		}
	}

	return ServerStatus{
		Running:   running,
		Addr:      addr,
		Host:      cfg.Host,
		Port:      cfg.Port,
		Public:    cfg.ServerConfig().IsPublic(),
		AutoStart: cfg.AutoStart,
		URLs:      accessURLs(cfg.Host, port),
	}, nil
}

// Start は現在の設定でサーバを起動する。
func (s *ServerService) Start() (ServerStatus, error) {
	cfg, err := settings.Load()
	if err != nil {
		return ServerStatus{}, err
	}
	if err := s.lib.Server().Start(cfg.ServerConfig()); err != nil {
		return ServerStatus{}, err
	}
	return s.Status()
}

// Stop はサーバを停止する。
func (s *ServerService) Stop() (ServerStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.lib.Server().Stop(ctx); err != nil {
		return ServerStatus{}, err
	}
	return s.Status()
}

// UpdateSettings は待ち受け設定を保存する。
// 稼働中に変更した場合は、新しい設定で listen し直す。
func (s *ServerService) UpdateSettings(host string, port int, autoStart bool) (ServerStatus, error) {
	if port <= 0 || port > 65535 {
		return ServerStatus{}, fmt.Errorf("ポートは 1〜65535 の範囲で指定してください: %d", port)
	}
	if host != "" && net.ParseIP(host) == nil && host != "localhost" {
		return ServerStatus{}, fmt.Errorf("bind アドレスが不正です: %s", host)
	}

	next := settings.Settings{Host: host, Port: port, AutoStart: autoStart}
	if err := settings.Save(next); err != nil {
		return ServerStatus{}, err
	}

	// 稼働中なら新しい設定を反映するために貼り直す。
	if running, _ := s.lib.Server().Running(); running {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.lib.Server().Stop(ctx); err != nil {
			return ServerStatus{}, err
		}
		if err := s.lib.Server().Start(next.ServerConfig()); err != nil {
			// 新しい設定で listen できなかった。状態は「停止」で返し、
			// 設定は保存済みなので UI から直して再開できる。
			return s.Status()
		}
	}
	return s.Status()
}

// KifuURLs は保存済み棋譜(kicho 自前の ID)の URL を返す。
// 外部ツールへ貼るための「URL コピー」で使う。先頭が localhost 版。
func (s *ServerService) KifuURLs(id string) ([]string, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("棋譜 ID が空です")
	}
	return s.urlsFor(httpapi.KifuPath(id))
}

// SourceURLs は取得元から直接取得する URL(ライブ経路)を返す。
// 対局中の棋譜はこちらを渡す(外部ツールが開くたびにサイトから取り直す)。
//
// サイトの URL ではなく kicho 経由の URL を返す。連盟の中継は Shift_JIS で
// 配信されているので、kicho を通して UTF-8 に寄せたものを渡すため。
func (s *ServerService) SourceURLs(source, sourceID string) ([]string, error) {
	if strings.TrimSpace(sourceID) == "" {
		return nil, fmt.Errorf("取得元の棋譜 ID が空です")
	}
	switch source {
	case store.SourceShogiLive:
		return s.urlsFor(httpapi.ShogiLiveKifuPath(sourceID))
	case store.SourceYomiuri, "":
		return s.urlsFor(httpapi.RyuohKifuPath(sourceID))
	default:
		return nil, fmt.Errorf("ライブ取得に対応していない取得元です: %s", source)
	}
}

func (s *ServerService) urlsFor(path string) ([]string, error) {
	st, err := s.Status()
	if err != nil {
		return nil, err
	}
	out := composeURLs(st.URLs, path)
	if len(out) == 0 {
		return nil, fmt.Errorf("サーバの URL を組み立てられません（ポート設定を確認してください）")
	}
	return out, nil
}

// composeURLs はベース URL 群にパスを連結する。
func composeURLs(bases []string, path string) []string {
	out := make([]string, 0, len(bases))
	for _, base := range bases {
		out = append(out, base+path)
	}
	return out
}

// accessURLs は外部ツールに貼れるベース URL を組み立てる。
// LAN 公開時は実際に到達できる LAN アドレスも併記する。
func accessURLs(host string, port int) []string {
	if port <= 0 {
		return nil
	}
	p := strconv.Itoa(port)
	urls := []string{"http://" + net.JoinHostPort("127.0.0.1", p)}

	if ip := net.ParseIP(host); ip == nil || ip.IsLoopback() {
		return urls
	}
	// 0.0.0.0 等で公開している場合、実際に到達できる LAN アドレスを出す。
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return urls
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		urls = append(urls, "http://"+net.JoinHostPort(ipnet.IP.String(), p))
	}
	return urls
}
