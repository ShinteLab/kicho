module kicho-app

go 1.26.1

require github.com/wailsapp/wails/v3 v3.0.0-beta.23

require (
	github.com/dlclark/regexp2/v2 v2.5.2 // indirect
	github.com/dop251/goja v0.0.0-20260723142020-b4aef50fa347 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.54.0 // indirect
)

require (
	github.com/ShinteLab/core v0.0.0-00010101000000-000000000000 // indirect
	github.com/ShinteLab/kicho v0.0.0-00010101000000-000000000000
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/ShinteLab/core => ../../../core

replace github.com/ShinteLab/kicho => ../../
