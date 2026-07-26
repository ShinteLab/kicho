package main

import (
	"context"
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/settings"
)

// frontend/dist をバイナリに埋め込む。
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// 棋譜 DB を開く(既定は os.UserConfigDir()/kicho/kicho.db)。
	lib, err := kicho.Open("", logger)
	if err != nil {
		logger.Error("棋譜データベースを開けませんでした", "error", err)
		os.Exit(1)
	}
	defer lib.Close(context.Background())

	app := application.New(application.Options{
		Name:        "kicho",
		Description: "kicho - 棋帳",
		Logger:      logger,
		Services: []application.Service{
			application.NewService(NewKifuService(lib)),
			application.NewService(NewServerService(lib)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "kicho - 棋帳",
		Width:  1100,
		Height: 720,
		// フロントを描くまでの地の色。app.css の既定テーマ(ダーク)の --bg と
		// 揃えておかないと起動時に一瞬明るい色が出る。テーマを変えたらここも直す。
		BackgroundColour: application.NewRGB(22, 24, 28),
		URL:              "/",
	})

	// 設定に応じて起動時に HTTP サーバも立ち上げる。
	// 失敗してもアプリは動かす(UI の「サーバ」タブから直せる)。
	if cfg, err := settings.Load(); err != nil {
		logger.Error("設定を読み込めませんでした", "error", err)
	} else if cfg.AutoStart {
		if err := lib.Server().Start(cfg.ServerConfig()); err != nil {
			logger.Error("HTTP サーバを起動できませんでした", "error", err)
		}
	}

	if err := app.Run(); err != nil {
		logger.Error("アプリが異常終了しました", "error", err)
		os.Exit(1)
	}
}
