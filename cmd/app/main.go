package main

import (
	"context"
	"fmt"
	"os"

	"go-template/internal/app"
	"go-template/internal/config"
	"go-template/internal/logger"
	"go-template/internal/signal"
	"go-template/pkg/version"

	"github.com/spf13/pflag"
	"go.uber.org/zap"
)

func main() {
	// 定义命令行参数
	var (
		showHelp    bool   // 是否显示帮助信息
		showVersion bool   // 是否显示版本信息
		initConfig  bool   // 是否生成默认配置文件
		outputFile  string // 生成的配置文件输出路径
		configPath  string // 配置文件路径
	)
	// 使用 pflag 以支持 POSIX 风格的 flag
	pflag.BoolVarP(&showHelp, "help", "h", false, "Show help message")
	pflag.BoolVarP(&showVersion, "version", "v", false, "Show version information")
	pflag.BoolVarP(&initConfig, "init", "i", false, "Generate default configuration file")
	pflag.StringVarP(&outputFile, "output", "o", "", "Output file path for generated config")

	// 默认配置文件路径：打包后使用 config.yaml，开发时使用 configs/config.yaml
	defaultConfigPath := "config.yaml"
	if _, err := os.Stat("configs/config.yaml"); err == nil {
		defaultConfigPath = "configs/config.yaml"
	}
	pflag.StringVarP(&configPath, "config", "c", defaultConfigPath, "Config file path")
	pflag.Parse()

	// 显示帮助信息并退出
	if showHelp {
		fmt.Println("Usage: app [options]")
		fmt.Println("\nOptions:")
		pflag.PrintDefaults()
		os.Exit(0)
	}

	// 显示版本信息并退出
	if showVersion {
		version.Print()
		os.Exit(0)
	}

	// 生成默认配置文件
	if initConfig {
		config.GenerateConfig(outputFile)
		os.Exit(0)
	}

	if err := config.Init(configPath); err != nil {
		fmt.Printf("Failed to init config: %v\n", err)
		os.Exit(1)
	}

	cfg := config.Get()
	if err := logger.Init(&cfg.Log); err != nil {
		fmt.Printf("Failed to init logger: %v\n", err)
		os.Exit(1)
	}

	config.AddWatch(func(newCfg, oldCfg *config.Config) {
		if newCfg.Log != oldCfg.Log {
			if err := logger.Reset(&newCfg.Log); err != nil {
				logger.Error("Failed to reset logger", zap.Error(err))
				return
			}
			logger.Info("Logger config updated dynamically")
		}
	})

	if _, err := config.StartWatcher(); err != nil {
		logger.Warn("Failed to start config watcher", zap.Error(err))
	}

	ctx := signal.ContextWithShutdown(context.Background())

	logger.Info("Application initialized",
		zap.String("config", configPath),
		zap.String("name", cfg.App.Name),
		zap.String("env", cfg.App.Env),
	)

	if err := app.Run(ctx); err != nil {
		logger.Error("Application error", zap.Error(err))
	}

	cleanup()
}

func cleanup() {
	logger.Info("Cleaning up resources...")
	logger.Sync()
}
