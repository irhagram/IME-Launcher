package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "IME Launcher",
		Width:            1040,
		Height:           705,
		DisableResize:    true,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 12, G: 13, B: 14, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			WebviewUserDataPath:  app.dataDir("webview"),
			Theme:                windows.Dark,
			DisableWindowIcon:    false,
			WebviewIsTransparent: false,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
