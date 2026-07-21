package webapi

import (
	"asa-server/app"
	"asa-server/batchmanage"
	cfgpkg "asa-server/config"
	"asa-server/frpmanage"
	"asa-server/logger"
	"asa-server/parseserver"
	"asa-server/realtime"
	"asa-server/schedule"
	statepkg "asa-server/state"
	"asa-server/syncthingmanage"
	"asa-server/updatemanage"
	"asa-server/webapi/backupapi"
	"asa-server/webapi/configapi"
	"asa-server/webapi/iconapi"
	"asa-server/webapi/instanceapi"
	"asa-server/webapi/logapi"
	"asa-server/webapi/saveapi"
	"asa-server/webapi/scheduleapi"
	"asa-server/webapi/serverapi"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/urfave/cli/v3"
)

// APIServer represents the HTTP API server for ARK Server Ascended Instance Management
type APIServer struct {
	engine          *gin.Engine
	port            int
	serverCtx       context.Context
	serverCtxStop   func()
	serverDone      chan struct{}
	saveDataManager *parseserver.SaveDataManager
}

var ApiServerPort = 19193

// NewAPIServer creates a new API server instance
func NewAPIServer() *APIServer {
	hub := realtime.NewHub()
	realtime.SetGlobalHub(hub)
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
		engine:          engine,
		port:            ApiServerPort,
		serverCtx:       ctx,
		serverCtxStop:   cancel,
		serverDone:      make(chan struct{}, 1),
		saveDataManager: saveDataManager,
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

	if err := statepkg.InitStateManager(cfgpkg.BaseDir); err != nil {
		panic(err)
	}
	s.startStateChangeDispatcher(s.serverCtx)

	// 定时调度必须在状态管理器之后启动：批量启停依赖 state 的 CAS
	if sched := schedule.GetGlobalScheduler(); sched != nil {
		sched.Start()
	}

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

	// Stop the schedule loop before the batch manager: a tick could otherwise
	// kick off a new batch operation right as we are shutting one down
	if sched := schedule.GetGlobalScheduler(); sched != nil {
		sched.Stop()
	}

	// Stop batch manager
	if mgr := batchmanage.GetGlobalManager(); mgr != nil {
		mgr.Shutdown()
	}

	// 1. 先取消 server context，触发 SSE handler 退出
	s.serverCtxStop()

	// 2. 关闭所有 WebSocket 长连接
	realtime.GetGlobalHub().CloseAllClients()

	<-s.serverDone
	log.Println("stopping saveDataManager ...")
	// 3. 所有 in-flight 请求完成后，关闭数据层
	if s.saveDataManager != nil {
		s.saveDataManager.Stop()
	}

	log.Println("saveDataManager stopped")
	if err := statepkg.CloseStateManager(); err != nil {
		logger.GetLogger().Warnf("Error closing state manager: %v", err)
	}

	return nil
}

// setupRoutes configures all API endpoints
func (s *APIServer) setupRoutes() {

	// Domain handlers register their own routes (each package exposes RegisterRouter)
	instanceapi.NewHandler().RegisterRouter(s.engine)
	serverapi.NewHandler(s.serverCtx).RegisterRouter(s.engine)
	backupapi.NewHandler().RegisterRouter(s.engine)
	configapi.NewHandler().RegisterRouter(s.engine)
	saveapi.NewHandler(s.serverCtx, s.saveDataManager).RegisterRouter(s.engine)
	logapi.NewHandler(s.serverCtx).RegisterRouter(s.engine)
	iconapi.NewHandler().RegisterRouter(s.engine)
	scheduleapi.NewHandler().RegisterRouter(s.engine)

	// WebSocket endpoints
	s.engine.GET("/api/ws/events", realtime.HandleServerEvents)
	s.engine.GET("/api/ws/rcon", realtime.HandleRCONEvents)

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
	if _, err := frpmanage.Initialize(cfgpkg.BaseDir); err != nil {
		log.Fatal(err)
	}
	// Initialize syncthing manager
	if _, err := syncthingmanage.Initialize(cfgpkg.BaseDir); err != nil {
		log.Fatal(err)
	}
	// Initialize batch manager
	batchmanage.Initialize()
	// Initialize update manager
	updatemanage.Initialize()
	// Initialize schedule scheduler (loads persisted tasks; Start() begins the loop)
	if err := schedule.Initialize(); err != nil {
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
	log.Printf("shutting down... \n")
	return apiServer.Stop()
}
