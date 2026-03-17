package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig      `yaml:"server"`
	Prometheus PrometheusConfig  `yaml:"prometheus"`
	Devices    map[string]string `yaml:"devices,omitempty"`
}

type ServerConfig struct {
	Port     int      `yaml:"port"`
	Interval Duration `yaml:"interval"`
}

type PrometheusConfig struct {
	URL string `yaml:"url"`
	Job string `yaml:"job"`
}

// Duration は time.Duration の YAML ラウンドトリップ対応ラッパー
type Duration time.Duration

func (d Duration) Unwrap() time.Duration { return time.Duration(d) }

func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	dur, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("duration 解析エラー %q: %w", value.Value, err)
	}
	*d = Duration(dur)
	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("設定ファイルを読めません: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("YAML解析エラー: %w", err)
	}

	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Interval == 0 {
		cfg.Server.Interval = Duration(3 * time.Second)
	}
	if cfg.Server.Interval.Unwrap() <= 0 {
		return nil, fmt.Errorf("server.interval は正の値が必要です: %s", cfg.Server.Interval.Unwrap())
	}
	if cfg.Prometheus.URL == "" {
		cfg.Prometheus.URL = "http://localhost:9090"
	}
	if cfg.Prometheus.Job == "" {
		cfg.Prometheus.Job = "node"
	}
	if cfg.Devices == nil {
		cfg.Devices = make(map[string]string)
	}

	return &cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("YAML生成エラー: %w", err)
	}

	tmp := path + ".tmp"
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
