package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nodeox/nodepass/internal/agent"
	"github.com/nodeox/nodepass/internal/observability"
	"go.uber.org/zap"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// 命令行参数
	var (
		configFile = flag.String("config", "config.yaml", "配置文件路径")
		runAs      = flag.String("run-as", "", "运行角色: ingress, egress, relay")
		version    = flag.Bool("version", false, "显示版本信息")
		validate   = flag.Bool("validate", false, "验证配置文件")
	)
	flag.Parse()

	// 显示版本
	if *version {
		fmt.Printf("NodePass %s (built at %s)\n", Version, BuildTime)
		os.Exit(0)
	}

	// 初始化日志
	logger, err := observability.NewLogger("info", "json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting nodepass",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("role", *runAs),
		zap.String("config", *configFile),
	)

	// 加载配置
	cfg, err := agent.LoadConfig(*configFile)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	// 如果指定了角色，覆盖配置
	if *runAs != "" {
		cfg.Node.Type = *runAs
	}

	// 如果只是验证配置
	if *validate {
		logger.Info("configuration is valid")
		os.Exit(0)
	}

	// 创建 Agent
	ag, err := agent.New(cfg, logger)
	if err != nil {
		logger.Fatal("failed to create agent", zap.Error(err))
	}

	// 设置配置文件路径（启用配置热更新）
	ag.SetConfigPath(*configFile)

	// 启动 Agent
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ag.Start(ctx); err != nil {
		logger.Fatal("failed to start agent", zap.Error(err))
	}

	logger.Info("agent started successfully (placeholder)")

	// 等待信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("received signal, shutting down", zap.String("signal", sig.String()))

	// 优雅关闭
	if err := ag.Stop(); err != nil {
		logger.Error("failed to stop agent", zap.Error(err))
	}

	logger.Info("agent stopped")
}
