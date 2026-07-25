// Package settings は kicho の設定と保存先パスを扱う。
//
// Wails には依存しない。設定は os.UserConfigDir()/kicho/settings.json に置き、
// 棋譜 DB も同じディレクトリに作る。
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ShinteLab/kicho/httpapi"
)

// AppName は設定・データを置くディレクトリ名。
const AppName = "kicho"

const (
	settingsFile = "settings.json"
	databaseFile = "kicho.db"
)

// Settings は永続化する設定。
type Settings struct {
	// Host は HTTP サーバの bind アドレス。
	// 既定はループバック。0.0.0.0 にすると LAN に公開される。
	Host string `json:"host"`
	// Port は HTTP サーバのポート。
	Port int `json:"port"`
	// AutoStart はアプリ起動時に HTTP サーバも起動するかどうか。
	AutoStart bool `json:"autoStart"`
}

// Default は既定の設定を返す。
func Default() Settings {
	c := httpapi.DefaultConfig()
	return Settings{Host: c.Host, Port: c.Port, AutoStart: true}
}

// ServerConfig は Settings を httpapi の待ち受け設定に変換する。
func (s Settings) ServerConfig() httpapi.Config {
	return httpapi.Config{Host: s.Host, Port: s.Port}
}

// Dir は設定・データを置くディレクトリを返す(無ければ作る)。
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	dir := filepath.Join(base, AppName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// DatabasePath は棋譜 DB のパスを返す。
func DatabasePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, databaseFile), nil
}

// Path は設定ファイルのパスを返す。
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, settingsFile), nil
}

// Load は設定を読み込む。ファイルが無ければ Default() を返す(エラーにしない)。
// 壊れた JSON はエラーにする(黙って既定値に戻すと設定消失に気付けないため)。
func Load() (Settings, error) {
	path, err := Path()
	if err != nil {
		return Default(), err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), fmt.Errorf("read settings: %w", err)
	}

	s := Default()
	if err := json.Unmarshal(stripBOM(b), &s); err != nil {
		return Default(), fmt.Errorf("parse settings %s: %w", path, err)
	}
	return s.normalized(), nil
}

// utf8BOM は UTF-8 のバイトオーダーマーク。
// Windows のメモ帳などは既定で BOM を付けて保存するため、
// 設定ファイルを手で編集されても読めるように取り除く
// (encoding/json は BOM を受け付けない)。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, utf8BOM)
}

// Save は設定を書き出す。
func Save(s Settings) error {
	path, err := Path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.normalized(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// normalized は不正な値を既定値に寄せる。
func normalizedOf(s Settings) Settings {
	d := Default()
	if s.Host == "" {
		s.Host = d.Host
	}
	// 0 は「OS 任せ」になり UI から再現できないので既定ポートに寄せる。
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = d.Port
	}
	return s
}

func (s Settings) normalized() Settings { return normalizedOf(s) }
