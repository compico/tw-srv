module github.com/compico/overlaycomposer

go 1.26.0

// Здесь только прямые зависимости, которые я использую явно. Версии grpc/protobuf и все
// indirect-зависимости проставит `go mod tidy` (см. `make setup`), после чего закоммитьте go.sum.
require (
	github.com/fasthttp/websocket v1.5.12
	github.com/glebarez/sqlite v1.11.0
	github.com/gofiber/fiber/v3 v3.5.0
	github.com/olivere/vite v0.1.0
	gorm.io/gorm v1.31.2
)
