package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	// Video proxy: accepts either session auth (dashboard), token auth (API clients), or public completed streams
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
		videoProxyRouter.HEAD("/videos/:task_id/content", controller.VideoProxy)
	}

	videoSharedRouter := router.Group("/v1")
	videoSharedRouter.Use(middleware.RouteTag("relay"))
	videoSharedRouter.Use(middleware.TokenAuth())
	videoSharedRouter.Use(middleware.SystemPerformanceCheck())
	videoSharedRouter.POST(
		"/video/generations",
		middleware.PinTaskPluginEndpoint(),
		middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()),
		middleware.PrepareTaskPluginEndpoint(),
		middleware.Distribute(),
		func(c *gin.Context) {
			controller.RelayTaskPluginEndpoint(c, controller.RelayTask)
		},
	)

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth())
	{
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", middleware.Distribute(), controller.RelayTask)
		videoV1Router.POST("/videos", middleware.Distribute(), controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/contents/generations/tasks", middleware.Distribute(), controller.RelayTask)
		videoV1Router.GET("/contents/generations/tasks/:task_id", controller.RelayTaskFetch)
	}

	doubaoV3Router := router.Group("/api/v3")
	doubaoV3Router.Use(middleware.RouteTag("relay"))
	doubaoV3Router.Use(middleware.TokenAuth())
	{
		doubaoV3Router.POST("/contents/generations/tasks", middleware.Distribute(), controller.RelayTask)
		doubaoV3Router.GET("/contents/generations/tasks/:task_id", controller.RelayTaskFetch)
	}

	doubaoRootRouter := router.Group("/contents")
	doubaoRootRouter.Use(middleware.RouteTag("relay"))
	doubaoRootRouter.Use(middleware.TokenAuth())
	{
		doubaoRootRouter.POST("/generations/tasks", middleware.Distribute(), controller.RelayTask)
		doubaoRootRouter.GET("/generations/tasks/:task_id", controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.TokenAuth())
	{
		klingV1Router.POST("/videos/text2video", middleware.Distribute(), controller.RelayTask)
		klingV1Router.POST("/videos/image2video", middleware.Distribute(), controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", controller.RelayTask)
	}
}
