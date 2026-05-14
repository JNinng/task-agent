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

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func main() {
	var (
		initConfig bool
		outputFile string
		configPath string
	)

	// 默认配置文件路径：打包后使用 config.yaml，开发时使用 configs/config.yaml
	defaultConfigPath := "config.yaml"
	if _, err := os.Stat("configs/config.yaml"); err == nil {
		defaultConfigPath = "configs/config.yaml"
	}

	rootCmd := &cobra.Command{
		Use:   "app",
		Short: "Go application template",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 生成默认配置文件
			if initConfig {
				config.GenerateConfig(outputFile)
				os.Exit(0)
			}

			cfg, err := config.Init(configPath)
			if err != nil {
				return fmt.Errorf("failed to init config: %w", err)
			}

			if err := logger.Init(&cfg.Log); err != nil {
				return fmt.Errorf("failed to init logger: %w", err)
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
			return nil
		},
	}

	rootCmd.Flags().BoolVarP(&initConfig, "init", "i", false, "Generate default configuration file")
	rootCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file path for generated config")
	rootCmd.Flags().StringVarP(&configPath, "config", "c", defaultConfigPath, "Config file path")

	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			version.Print()
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func cleanup() {
	logger.Info("Cleaning up resources...")
	logger.Sync()
}
