package webapi

import (
	"asa-server/app"
	"asa-server/asaserver"
	"asa-server/frpmanage"
	"asa-server/logger"
	"asa-server/syncthingmanage"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/urfave/cli/v3"
)

// APIServer represents the HTTP API server for ARK Server Ascended Instance Management
type APIServer struct {
	engine  *gin.Engine
	port    int
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
	// Task broadcasters for independent SSE streams
	updateBroadcaster         *TaskBroadcaster
	startBroadcaster          *TaskBroadcaster
	stopBroadcaster           *TaskBroadcaster
	restartBroadcaster        *TaskBroadcaster
	instanceStartBroadcasters *InstanceStartBroadcasters // Per-instance startup broadcasters
	serverCtx                 context.Context
	serverCtxStop             func()
	serverDone                chan struct{}
}

var serverActionsLock sync.Mutex

var ApiServerPort = 19193

var globalAPIServer *APIServer

// NewAPIServer creates a new API server instance
func NewAPIServer() *APIServer {
	InitializationBasicComponents()
	gin.SetMode(gin.ReleaseMode)
	engine := gin.Default()
	engine.Use(cors.Default())
	ctx, cancel := context.WithCancel(context.Background())
	server := &APIServer{
		engine:                    engine,
		port:                      ApiServerPort,
		clients:                   make(map[*websocket.Conn]bool),
		updateBroadcaster:         NewTaskBroadcaster(),
		startBroadcaster:          NewTaskBroadcaster(),
		stopBroadcaster:           NewTaskBroadcaster(),
		restartBroadcaster:        NewTaskBroadcaster(),
		instanceStartBroadcasters: NewInstanceStartBroadcasters(),
		serverCtx:                 ctx,
		serverCtxStop:             cancel,
		serverDone:                make(chan struct{}, 1),
	}

	// Setup routes
	server.setupRoutes()
	// Set global API server instance
	globalAPIServer = server

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
	shutdownCtx, shutdowncancel := context.WithTimeout(context.Background(), 30*time.Second)
	shutdowncancel()
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
	s.serverCtxStop()
	<-s.serverDone
	return nil
}

// setupRoutes configures all API endpoints
func (s *APIServer) setupRoutes() {
	distFs := app.GetDistFs()
	fs, err := static.EmbedFolder(distFs, "dist")
	if err != nil {
		panic(err)
	}
	appVue := func() gin.HandlerFunc {
		return static.Serve("/", fs)
	}
	//s.engine.StaticFS("/", http.FS(distFs))
	s.engine.Use(appVue())

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
		server.POST("/:name/start", s.startServer)
		server.POST("/:name/stop", s.stopServer)
		server.POST("/:name/restart", s.restartServer)
		server.POST("/start-all", s.startAllServers)
		server.POST("/stop-all", s.stopAllServers)
		server.POST("/restart-all", s.restartAllServers)
	}

	// RCON endpoints
	rcon := s.engine.Group("/api/rcon")
	{
		rcon.POST("/:name/command", s.sendRCONCommand)
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

	// Server update endpoints
	s.engine.POST("/api/server/update", s.handleServerUpdate)

	// Server info endpoints
	s.engine.GET("/api/server/info", s.streamServerInfo)
	s.engine.GET("/api/server/:name/info", s.streamInstanceInfo)
	s.engine.GET("/api/server/all-info", s.streamAllInstancesInfo)

	// Mod info endpoint
	s.engine.GET("/api/mod-info", s.getModInfo)
	// WebSocket endpoints
	s.engine.GET("/api/ws/events", s.handleServerEvents)
	s.engine.GET("/api/ws/rcon", s.handleRCONEvents)

	// FRP endpoints
	frpmanage.RegisterFRPRoutes(s.engine)
	syncthingmanage.RegisterSyncthingRoutes(s.engine)

	s.engine.NoRoute(func(c *gin.Context) {
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
	return apiServer.Stop()
}
