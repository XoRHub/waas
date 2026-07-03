module github.com/xorhub/waas/wwt

go 1.25.3

require (
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/gorilla/websocket v1.5.3
	github.com/xorhub/waas/shared v0.0.0
)

replace github.com/xorhub/waas/shared => ../shared
