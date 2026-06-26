package webapi

import (
	"asa-server/app"
	"asa-server/asaserver"
	"asa-server/batchmanage"
	"asa-server/frpmanage"
	"asa-server/httpserver"
	"asa-server/logger"
	"asa-server/parseserver"
	"asa-server/syncthingmanage"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/urfave/cli/v3"
)

// APIServer represents the HTTP API server for ARK Server Ascended Instance Management
type APIServer struct {
	engine *gin.Engine
	port   int
	// Task broadcasters for independent SSE streams
	updateBroadcaster *httpserver.TaskBroadcaster
	updateCtx         context.Context    // 当前更新任务的 context
	updateCancel      context.CancelFunc // 取消函数
	updateMu          sync.Mutex         // 保护 updateCtx/updateCancel
	serverCtx         context.Context
	serverCtxStop     func()
	serverDone        chan struct{}
	saveDataManager   *parseserver.SaveDataManager
}

var ApiServerPort = 19193

// NewAPIServer creates a new API server instance
func NewAPIServer() *APIServer {
	hub := httpserver.NewHub()
	httpserver.SetGlobalHub(hub)
	InitializationBasicComponents()
	gin.SetMode(gin.ReleaseMode)
	engine := gin.Default()
	engine.Use(cors.Default())
	ctx, cancel := context.WithCancel(context.Background())
	saveDataManager, err := parseserver.NewSaveDataManager()
	if err != nil {
		logger.GetLogger().Warnf("Failed to init save data manager: %v", err)
	}

	server := &APIServer{
		engine:            engine,
		port:              ApiServerPort,
		updateBroadcaster: httpserver.NewTaskBroadcaster(),
		serverCtx:         ctx,
		serverCtxStop:     cancel,
		serverDone:        make(chan struct{}, 1),
		saveDataManager:   saveDataManager,
	}

	// Setup routes
	server.setupRoutes()

	return server
}

// Start starts the API server and frpc
func (s *APIServer) Start() error {
	// Start frpc manager
	frpcMgr := frpmanage.GetGlobalManager()
	if frpcMgr != nil {
		if err := frpcMgr.Start(); err != nil {
			logger.GetLogger().Errorf("Failed to start frpc: %v", err)
		}
	}
	//start syncthing manage
	syncthingMgr := syncthingmanage.GetGlobalManager()
	if syncthingMgr != nil {
		if err := syncthingMgr.Start(); err != nil {
			logger.GetLogger().Errorf("Failed to start syncthing: %v", err)
		}
	}

	if err := asaserver.InitStateManager(asaserver.BaseDir); err != nil {
		panic(err)
	}
	s.startStateChangeDispatcher(s.serverCtx)

	// Start save file monitor
	if s.saveDataManager != nil {
		go s.saveDataManager.Start(s.serverCtx)
	}

	// Start listening on port
	addr := fmt.Sprintf(":%d", s.port)
	srv := &http.Server{
		Addr:    addr,
		Handler: s.engine,
	}

	go func() {
		// service connections
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.GetLogger().Errorf("Failed to listen and serve: %v", err)
			log.Fatalf("listen: %s\n", err)
		}
	}()

	logger.GetStdout().Infof("Starting API server on %s", addr)
	log.Printf("Starting API server on %s \n", addr)
	<-s.serverCtx.Done()
	log.Println("Shutdown Server ...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Println("Server Shutdown:", err)
	}
	log.Println("Server exiting")
	s.serverDone <- struct{}{}
	return nil
}

// Stop stops the API server and frpc
func (s *APIServer) Stop() error {
	// Stop frpc manager
	frpcMgr := frpmanage.GetGlobalManager()
	if frpcMgr != nil {
		if err := frpcMgr.Stop(); err != nil {
			logger.GetLogger().Warnf("Error stopping frpc: %v", err)
		}
	}
	syncthingMgr := syncthingmanage.GetGlobalManager()
	if syncthingMgr != nil {
		if err := syncthingMgr.Stop(); err != nil {
			logger.GetLogger().Warnf("Error stopping syncthing: %v", err)
		}
	}

	// Stop batch manager
	if mgr := batchmanage.GetGlobalManager(); mgr != nil {
		mgr.Shutdown()
	}

	// 1. 先取消 server context，触发 SSE handler 退出
	s.serverCtxStop()

	// 2. 关闭所有 WebSocket 长连接
	httpserver.GetGlobalHub().CloseAllClients()

	<-s.serverDone
	log.Println("stopping saveDataManager ...")
	// 3. 所有 in-flight 请求完成后，关闭数据层
	if s.saveDataManager != nil {
		s.saveDataManager.Stop()
	}

	log.Println("saveDataManager stopped")
	if err := asaserver.CloseStateManager(); err != nil {
		logger.GetLogger().Warnf("Error closing state manager: %v", err)
	}

	return nil
}

// setupRoutes configures all API endpoints
func (s *APIServer) setupRoutes() {

	s.engine.GET("/health", s.health)
	// Instance management endpoints
	instances := s.engine.Group("/api/instances")
	{
		instances.GET("", s.listInstances)
		instances.POST("", s.createInstance)
		instances.GET("/:name", s.getInstanceStatus)
		instances.DELETE("/:name", s.deleteInstance)
		instances.PUT("/:name", s.renameInstance)
		instances.GET("/:name/config", s.getInstanceConfig)
		instances.PATCH("/:name/config", s.updateInstanceConfig)
	}

	// Server control endpoints
	server := s.engine.Group("/api/server")
	{
		server.GET("/:name/start", s.startServer)
		server.GET("/:name/stop", s.stopServer)
		server.GET("/:name/restart", s.restartServer)
		server.GET("/:name/force-stop", s.forceStopServer)
		server.GET("/:name/info", s.streamInstanceInfo)

		// Batch operations (registered via batchmanage package)
		// Server update endpoints
		server.GET("/update", s.handleServerUpdate)
		server.GET("/update/status", s.getUpdateStatus)
		server.POST("/update/cancel", s.cancelUpdate)
		// Server info endpoints
		server.GET("/info", s.streamServerInfo)
		server.GET("/all-info", s.streamAllInstancesInfo)
	}

	// Backup/Restore endpoints
	backup := s.engine.Group("/api/backup")
	{
		backup.POST("/:name", s.backupInstance)
		backup.GET("", s.listBackups)
		backup.POST("/:name/restore", s.restoreBackup)
	}

	// Logs endpoints
	logs := s.engine.Group("/api/logs")
	{
		logs.GET("/:name", s.streamInstanceLogs)
		logs.GET("", s.streamSystemLogs)
	}

	// Config file endpoints
	config := s.engine.Group("/api/config")
	{
		config.GET("/server/configs", s.getServerConfigs)
		config.GET("/:name/configs", s.getInstanceConfigs)
		config.GET("/:name/game-ini", s.getGameIni)
		config.GET("/:name/game-user-settings", s.getGameUserSettings)
		config.POST("/:name/game-ini", s.uploadGameIni)
		config.POST("/:name/game-user-settings", s.uploadGameUserSettings)
		config.PUT("/:name/game-ini", s.updateGameIni)
		config.PUT("/:name/game-user-settings", s.updateGameUserSettings)
		config.POST("/sync", s.syncGameConfig)
		config.POST("/sync-instance", s.syncInstanceConfig)
	}

	// Save parse endpoints
	save := s.engine.Group("/api/save")
	{
		save.GET("/:instance/players", s.getSavePlayers)
		save.GET("/:instance/tribes", s.getSaveTribes)
		save.GET("/:instance/all", s.getSaveAll)
		save.GET("/:instance/stream", s.streamSaveData)
	}

	// Mod info endpoint
	s.engine.GET("/api/mod-info", s.getModInfo)
	// WebSocket endpoints
	s.engine.GET("/api/ws/events", httpserver.HandleServerEvents)
	s.engine.GET("/api/ws/rcon", httpserver.HandleRCONEvents)

	// FRP endpoints
	frpmanage.RegisterFRPRoutes(s.engine)
	syncthingmanage.RegisterSyncthingRoutes(s.engine)

	// Batch operation endpoints
	batchmanage.RegisterBatchRoutes(s.engine)

	distFs := app.GetDistFs()
	fs, err := static.EmbedFolder(distFs, "dist")
	if err != nil {
		panic(err)
	}

	appVue := func() gin.HandlerFunc {
		return static.Serve("/", fs)
	}
	s.engine.Use(appVue())

	s.engine.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}

		f, err := distFs.Open("dist/index.html")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		c.Data(200, "text/html; charset=utf-8", data)
	})
}

func InitializationBasicComponents() {
	// Initialize frpc manager
	if _, err := frpmanage.Initialize(asaserver.BaseDir); err != nil {
		log.Fatal(err)
	}
	// Initialize syncthing manager
	if _, err := syncthingmanage.Initialize(asaserver.BaseDir); err != nil {
		log.Fatal(err)
	}
	// Initialize batch manager
	batchmanage.Initialize()
}

// ActionAPI starts the HTTP API server
func ActionAPI(ctx context.Context, cmd *cli.Command) error {
	logger.SetLogMode(logger.HttpApiMode)
	apiServer := NewAPIServer()

	go func() {
		if err := apiServer.Start(); err != nil {
			log.Fatal(err)
		}
	}()

	ctx2, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	<-ctx2.Done()
	log.Printf("shutting down... \n")
	return apiServer.Stop()
}
