package nacos

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"go.yaml.in/yaml/v3"
)

// NacosConfig NacosConfig 配置结构体
type NacosConfig struct {
	Addr      string `yaml:"addr" mapstructure:"addr"`
	Port      uint64 `yaml:"port" mapstructure:"port"`
	Username  string `yaml:"username" mapstructure:"username"`
	Password  string `yaml:"password" mapstructure:"password"`
	Namespace string `yaml:"namespace" mapstructure:"namespace"`
	Group     string `yaml:"group" mapstructure:"group"`
	DataId    string `yaml:"data_id" mapstructure:"data_id"`
	LogLevel  string `yaml:"log_level" mapstructure:"log_level"`
	LogDir    string `yaml:"log_dir" mapstructure:"log_dir"`
	CacheDir  string `yaml:"cache_dir" mapstructure:"cache_dir"`
}

// DefaultConfig 默认 Nacos 配置
func DefaultConfig() *NacosConfig {
	return &NacosConfig{
		Addr:      "127.0.0.1",
		Port:      8848,
		Username:  "nacos",
		Password:  "nacos",
		Namespace: "public",
		Group:     "DEFAULT_GROUP",
		DataId:    "application.yml",
		LogLevel:  "debug",
		LogDir:    "./logs",
		CacheDir:  "./cache",
	}
}

var (
	client config_client.IConfigClient
	config atomic.Pointer[NacosConfig]
	once   sync.Once
)

func Init(cfg *NacosConfig, connectAfter, onChange func(namespace, group, dataId, data string)) {
	once.Do(func() {
		config.Store(cfg)
		clientConfig := constant.NewClientConfig(
			constant.WithUsername(cfg.Username),
			constant.WithPassword(cfg.Password),
			constant.WithLogLevel(cfg.LogLevel),
			constant.WithLogDir(cfg.LogDir),
			constant.WithCacheDir(cfg.CacheDir),
			constant.WithNotLoadCacheAtStart(true),
		)
		serverConfigs := []constant.ServerConfig{
			*constant.NewServerConfig(cfg.Addr, cfg.Port),
		}
		clientT, err := clients.NewConfigClient(
			vo.NacosClientParam{
				ClientConfig:  clientConfig,
				ServerConfigs: serverConfigs,
			},
		)
		if err != nil {
			return
		}
		client = clientT

		if onChange != nil {
			client.ListenConfig(vo.ConfigParam{
				DataId:   cfg.DataId,
				Group:    cfg.Group,
				OnChange: onChange,
			})
		}
		if connectAfter != nil {
			content, err := client.GetConfig(vo.ConfigParam{
				DataId: cfg.DataId,
				Group:  cfg.Group,
			})
			if err != nil {
				return
			}
			connectAfter(cfg.DataId, cfg.Group, cfg.Namespace, content)
		}
	})
}

// GetConfigContent 获取 Nacos 配置内容
func GetConfigContent() (string, error) {
	if client == nil {
		return "", errors.New("nacos client not initialized")
	}
	cfg := config.Load()
	content, err := client.GetConfig(vo.ConfigParam{
		DataId: cfg.DataId,
		Group:  cfg.Group,
	})
	return content, err
}

func GetConfig[T any]() (*T, error) {
	if client == nil {
		return nil, errors.New("nacos client not initialized")
	}
	content, err := GetConfigContent()
	if err != nil {
		return nil, err
	}

	var result T
	if err := yaml.Unmarshal([]byte(content), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
