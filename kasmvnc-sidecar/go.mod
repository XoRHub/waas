module github.com/xorhub/waas/kasmvnc-sidecar

go 1.26.0

require (
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674
	github.com/xorhub/waas/shared v0.0.0
)

require golang.org/x/net v0.61.0 // indirect

replace github.com/xorhub/waas/shared => ../shared
