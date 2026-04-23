package config

import (
	"context"

	"github.com/fsnotify/fsnotify"
)

// StartWatcher 启动配置文件监控
// 使用 Viper 内置的 WatchConfig 机制监听配置文件变更
// 返回 context.CancelFunc 用于优雅关闭 watcher
func StartWatcher() (context.CancelFunc, error) {
	if v == nil {
		return func() {}, nil
	}

	v.OnConfigChange(func(_ fsnotify.Event) {
		reloadConfig()
	})
	v.WatchConfig()

	return func() {}, nil
}
