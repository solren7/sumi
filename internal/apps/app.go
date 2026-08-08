package apps

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sumi/config"
	"sumi/internal/database"
	"sumi/internal/handlers"
	"sumi/internal/repository/dbgen"
	"sumi/internal/services"
	"sumi/middleware"
	"sumi/pkg/logx"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

// NewFiberApp assembles the HTTP app (middleware, handlers, routes) without
// starting it or touching the process lifecycle, so tests can drive the real
// stack through app.Test without binding a port.
func NewFiberApp(cfg *config.Config, svc *services.Service) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "MyFiberApp",
		ReadTimeout:  10 * time.Second,
		ErrorHandler: middleware.ErrorHandler,
	})
	app.Use(recover.New(recover.Config{
		// 是否开启堆栈跟踪 (默认 false)
		// 开启后，控制台会打印详细的错误堆栈，方便调试
		EnableStackTrace: true,
		StackTraceHandler: func(c fiber.Ctx, e any) {
			logx.Panicf("Panic: %v", e)
		},
	}))

	handler := handlers.NewHandler(svc, cfg)
	RegisterRoutes(app, handler, cfg)
	return app
}

func StartAPIServer(cfg *config.Config) {
	logx.Configure(cfg.LogFormat)

	// 1. 初始化数据库 (增加错误处理)
	logx.Info("Connecting to database...")
	// 注意：这里的 context 仅用于控制"连接超时"，连接成功后 dbPool 依然可用
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	dbPool, err := database.NewPool(dbCtx, cfg)
	dbCancel() // 及时释放 context 资源
	if err != nil {
		logx.WithError(err).Fatal("Failed to connect to Database")
	}

	if cfg.AutoMigrate {
		logx.Info("Running database migrations...")
		migrateCtx, migrateCancel := context.WithTimeout(context.Background(), cfg.MigrationTimeout)
		if err := database.RunMigrations(migrateCtx, cfg); err != nil {
			migrateCancel()
			logx.WithError(err).Fatal("Failed to run database migrations")
		}
		migrateCancel()
		logx.Info("Database migrations completed.")
	}

	// 2. Initialize Redis
	logx.Info("Connecting to redis...")
	rdb, err := database.NewRedis(context.Background(), cfg)
	if err != nil {
		logx.Fatal("Failed to connect to redis: " + err.Error())
	}
	defer rdb.Close()

	// 3. Initialize Services
	queries := dbgen.New(dbPool)
	svc := services.NewService(services.Deps{Pool: dbPool, Queries: queries, Config: cfg, Redis: rdb})

	// 4. Assemble handlers and routes
	app := NewFiberApp(cfg, svc)

	// 3. 准备启动 HTTP 服务
	// 创建一个专门接收 Server 启动错误的通道
	serverShutdown := make(chan struct{}) // 用于标记是否需要执行后续清理

	go func() {
		logx.Infof("Server is starting on %s", cfg.ServerPort)
		if err := app.Listen(cfg.ServerPort); err != nil {
			// 只有非关闭导致的错误才认为是启动失败
			logx.Errorf("Server Listen error: %v", err)
			// 这里不需要处理退出，因为 error 会导致这个协程结束
			// 关键是：主协程怎么知道？见下文 select
			// 发送一个系统中断信号给自己，触发主协程的 quit
			p, _ := os.FindProcess(os.Getpid())
			p.Signal(syscall.SIGINT)
		}
		close(serverShutdown) // 标记 server 协程已退出
	}()

	// 4. 监听信号 (主阻塞点)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// 阻塞在这里，等待 (Ctrl+C) 或者 (Server启动失败发的信号)
	<-quit
	logx.Info("Shutdown signal received...")

	// 5. 优雅关闭流程
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A. 关闭 Fiber (停止接收新请求)
	// ShutdownWithContext 会通知上面的 go func 停止 Listen，并处理完当前请求
	logx.Info("Shutting down Fiber...")
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		logx.WithError(err).Error("Fiber shutdown error")
	}

	// 等待子协程完全退出 (可选，但推荐)
	<-serverShutdown

	// B. 关闭数据库
	logx.Info("Closing database pool...")
	dbPool.Close()

	// C. 关闭 Redis
	logx.Info("Closing Redis connection...")
	if err := rdb.Close(); err != nil {
		logx.WithError(err).Error("Failed to close Redis connection")
	}

	logx.Info("Server exited properly.")
}
