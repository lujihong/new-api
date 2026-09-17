package router

import (
	"embed"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
}

func isStaticAssetPath(path string) bool {
	if path == "/static" || path == "/assets" ||
		strings.HasPrefix(path, "/static/") ||
		strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/favicon") ||
		strings.HasPrefix(path, "/logo") ||
		strings.HasPrefix(path, "/apple-touch-icon") ||
		strings.HasPrefix(path, "/android-chrome") {
		return true
	}

	dot := strings.LastIndex(path, ".")
	if dot < 0 {
		return false
	}
	switch strings.ToLower(path[dot:]) {
	case ".js", ".mjs", ".css", ".map", ".json", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".woff", ".woff2", ".ttf", ".eot":
		return true
	default:
		return false
	}
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		gzip.Gzip(gzip.DefaultCompression),
		middleware.Cache(),
		func(c *gin.Context) {
			if c.Request.URL.Path == "/static" || c.Request.URL.Path == "/assets" {
				controller.RelayNotFound(c)
				c.Abort()
				return
			}
			c.Next()
		},
		static.Serve("/", frontendFS),
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/v1") ||
				strings.HasPrefix(c.Request.URL.Path, "/api") ||
				isStaticAssetPath(c.Request.URL.Path) {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)
}
