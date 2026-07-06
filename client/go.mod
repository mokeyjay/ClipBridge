module github.com/mokeyjay/clipbridge/client

go 1.26

require (
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/mokeyjay/clipbridge/shared v0.0.0
	github.com/wailsapp/wails/v3 v3.0.0-alpha2.106
	golang.design/x/clipboard v0.7.1
	golang.org/x/sys v0.43.0
)

require (
	git.sr.ht/~jackmordaunt/go-toast/v2 v2.0.3 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/wailsapp/wails/webview2 v1.0.27 // indirect
	golang.org/x/exp/shiny v0.0.0-20250606033433-dcc06ee1d476 // indirect
	golang.org/x/image v0.40.0 // indirect
	golang.org/x/mobile v0.0.0-20250606033058-a2a15c67f36f // indirect
)

replace github.com/mokeyjay/clipbridge/shared => ../shared
