package config

// Source 表示外部配置源（如 Nacos、Consul 等）
type Source interface {
	Name() string
	Init() (content []byte, changes <-chan []byte, err error)
	Close() error
}
