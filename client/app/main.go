package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// main 密码本客户端 UI 壳（§14.1 1.8）。
// 支持附加参数 --reinit：重启初始化配置引导（忽略已存服务端地址，§9.2）。
// --admin：管理员模式（登录后进入管理面板，而非密码本界面）。
func main() {
	app := NewApp(dataDirFromArgs())

	// --reinit 启动参数：前端据此强制走首次引导页
	app.reinit = hasArg("--reinit")
	// --admin 启动参数：管理员模式
	app.adminMode = hasArg("--admin")

	err := wails.Run(&options.App{
		Title:     "在线密码本",
		Width:     1240,
		Height:    800,
		MinWidth:  980,
		MinHeight: 680,
		// frameless：无系统标题栏，由前端自绘标题栏与窗口控制（§14.1 窗口约定）
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 透明背景（玻璃拟态由前端绘制；A=0 让窗口底色透明）
		BackgroundColour: &options.RGBA{R: 10, G: 14, B: 26, A: 0},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			// 无 Aero 阴影与圆角（Win11），保证透明背景贴合自绘边框
			DisableFramelessWindowDecorations: true,
			Theme:                             windows.SystemDefault,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// hasArg 检查命令行参数是否包含指定项。
func hasArg(flag string) bool {
	for _, a := range os.Args[1:] {
		if a == flag {
			return true
		}
	}
	return false
}

// dataDirFromArgs 解析 --data <dir>（默认留空，NewApp 回退 ~/.passbook）。
func dataDirFromArgs() string {
	args := os.Args[1:]
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--data" {
			return args[i+1]
		}
	}
	return ""
}
