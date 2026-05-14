// Package nacos 提供 Nacos 配置中心的 Source 实现
package nacos

import (
	"errors"
	"sync"

	"go-template/internal/config"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"gopkg.in/yaml.v3"
)

type source struct {
	cfg    *config.NacosConfig
	client config_client.IConfigClient
	once   sync.Once
	err    error
}

// NewSource 创建一个 Nacos 配置源
// 返回的 Source 实现了 config.Source 接口
func NewSource(cfg *config.NacosConfig) config.Source {
	return &source{cfg: cfg}
}

// Name 返回配置源名称
func (s *source) Name() string { return "nacos" }

// Init 初始化 Nacos 客户端并返回初始配置内容和变更通道
//
// 返回:
//   - content: 初始配置内容
//   - changes: 配置变更通知通道
//   - err: 初始化失败时返回错误
func (s *source) Init() ([]byte, <-chan []byte, error) {
	changes := make(chan []byte, 8)

	s.once.Do(func() {
		clientCfg := constant.NewClientConfig(
			constant.WithUsername(s.cfg.Username),
			constant.WithPassword(s.cfg.Password),
			constant.WithLogLevel(s.cfg.LogLevel),
			constant.WithLogDir(s.cfg.LogDir),
			constant.WithCacheDir(s.cfg.CacheDir),
			constant.WithNotLoadCacheAtStart(true),
		)
		serverCfgs := []constant.ServerConfig{
			*constant.NewServerConfig(s.cfg.Addr, s.cfg.Port),
		}

		c, err := clients.NewConfigClient(vo.NacosClientParam{
			ClientConfig:  clientCfg,
			ServerConfigs: serverCfgs,
		})
		if err != nil {
			s.err = err
			return
		}
		s.client = c

		err = c.ListenConfig(vo.ConfigParam{
			DataId: s.cfg.DataId,
			Group:  s.cfg.Group,
			OnChange: func(namespace, group, dataId, data string) {
				select {
				case changes <- []byte(data):
				default:
				}
			},
		})
		if err != nil {
			s.err = err
			return
		}
	})

	if s.err != nil {
		return nil, nil, s.err
	}

	content, err := s.client.GetConfig(vo.ConfigParam{
		DataId: s.cfg.DataId,
		Group:  s.cfg.Group,
	})
	if err != nil {
		return nil, nil, err
	}

	return []byte(content), changes, nil
}

// Close 关闭 Nacos 配置源
func (s *source) Close() error {
	return nil
}

// GetConfigContent 直接获取 Nacos 配置内容（不通过 Source 接口）
func GetConfigContent(client config_client.IConfigClient, cfg *config.NacosConfig) (string, error) {
	if client == nil {
		return "", errors.New("nacos client is nil")
	}
	return client.GetConfig(vo.ConfigParam{
		DataId: cfg.DataId,
		Group:  cfg.Group,
	})
}

// GetConfig 获取并解析 Nacos 配置为指定类型
func GetConfig[T any](client config_client.IConfigClient, cfg *config.NacosConfig) (*T, error) {
	content, err := GetConfigContent(client, cfg)
	if err != nil {
		return nil, err
	}

	var result T
	if err := yaml.Unmarshal([]byte(content), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
