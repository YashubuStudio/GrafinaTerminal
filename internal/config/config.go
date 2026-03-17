package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Devices    DevicesConfig    `yaml:"devices,omitempty"`
}

type ServerConfig struct {
	Port     int          `yaml:"port"`
	Interval Duration     `yaml:"interval"`
	BurnIn   BurnInConfig `yaml:"burn_in,omitempty"`
}

type BurnInConfig struct {
	Enabled            *bool    `yaml:"enabled,omitempty"`
	PixelShiftInterval Duration `yaml:"pixel_shift_interval,omitempty"`
	PixelShiftStep     int      `yaml:"pixel_shift_step,omitempty"`
	IdleDimAfter       Duration `yaml:"idle_dim_after,omitempty"`
	IdleBrightness     float64  `yaml:"idle_brightness,omitempty"`
}

type PrometheusConfig struct {
	URL string `yaml:"url"`
	Job string `yaml:"job"`
}

type DeviceConfig struct {
	Name     string `yaml:"name,omitempty"`
	Priority int    `yaml:"priority,omitempty"`
}

type DevicesConfig map[string]DeviceConfig

func (b BurnInConfig) EnabledValue() bool {
	if b.Enabled == nil {
		return true
	}
	return *b.Enabled
}

func (d *DevicesConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == 0 {
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("devices はマッピング形式である必要があります")
	}

	out := make(DevicesConfig, len(value.Content)/2)
	for i := 0; i < len(value.Content); i += 2 {
		instance := value.Content[i].Value
		deviceNode := value.Content[i+1]

		switch deviceNode.Kind {
		case yaml.ScalarNode:
			out[instance] = DeviceConfig{Name: deviceNode.Value}
		case yaml.MappingNode:
			var device DeviceConfig
			if err := deviceNode.Decode(&device); err != nil {
				return fmt.Errorf("devices.%s の解析エラー: %w", instance, err)
			}
			out[instance] = device
		default:
			return fmt.Errorf("devices.%s は文字列またはマッピングで指定してください", instance)
		}
	}

	*d = out
	return nil
}

func (d DevicesConfig) MarshalYAML() (interface{}, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}

	instances := make([]string, 0, len(d))
	for instance := range d {
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool {
		left := d[instances[i]]
		right := d[instances[j]]
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		leftName := left.Name
		if leftName == "" {
			leftName = instances[i]
		}
		rightName := right.Name
		if rightName == "" {
			rightName = instances[j]
		}
		if leftName != rightName {
			return leftName < rightName
		}
		return instances[i] < instances[j]
	})

	for _, instance := range instances {
		device := d[instance]
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: instance}
		valueNode := &yaml.Node{}
		if err := valueNode.Encode(device); err != nil {
			return nil, err
		}
		node.Content = append(node.Content, keyNode, valueNode)
	}
	return node, nil
}

func (d DevicesConfig) Upsert(instance, name string, priority int) {
	if d == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == instance {
		name = ""
	}
	d[instance] = DeviceConfig{
		Name:     name,
		Priority: priority,
	}
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
	if cfg.Server.BurnIn.Enabled == nil {
		enabled := true
		cfg.Server.BurnIn.Enabled = &enabled
	}
	if cfg.Server.BurnIn.PixelShiftInterval == 0 {
		cfg.Server.BurnIn.PixelShiftInterval = Duration(45 * time.Second)
	}
	if cfg.Server.BurnIn.PixelShiftStep == 0 {
		cfg.Server.BurnIn.PixelShiftStep = 2
	}
	if cfg.Server.BurnIn.IdleDimAfter == 0 {
		cfg.Server.BurnIn.IdleDimAfter = Duration(90 * time.Second)
	}
	if cfg.Server.BurnIn.IdleBrightness == 0 {
		cfg.Server.BurnIn.IdleBrightness = 0.65
	}
	if cfg.Server.BurnIn.PixelShiftInterval.Unwrap() < 0 {
		return nil, fmt.Errorf("server.burn_in.pixel_shift_interval は 0 以上が必要です: %s", cfg.Server.BurnIn.PixelShiftInterval.Unwrap())
	}
	if cfg.Server.BurnIn.PixelShiftStep < 0 || cfg.Server.BurnIn.PixelShiftStep > 8 {
		return nil, fmt.Errorf("server.burn_in.pixel_shift_step は 0-8 の範囲で指定してください: %d", cfg.Server.BurnIn.PixelShiftStep)
	}
	if cfg.Server.BurnIn.IdleDimAfter.Unwrap() < 0 {
		return nil, fmt.Errorf("server.burn_in.idle_dim_after は 0 以上が必要です: %s", cfg.Server.BurnIn.IdleDimAfter.Unwrap())
	}
	if cfg.Server.BurnIn.IdleBrightness <= 0 || cfg.Server.BurnIn.IdleBrightness > 1 {
		return nil, fmt.Errorf("server.burn_in.idle_brightness は 0 より大きく 1 以下で指定してください: %g", cfg.Server.BurnIn.IdleBrightness)
	}
	if cfg.Prometheus.URL == "" {
		cfg.Prometheus.URL = "http://localhost:9090"
	}
	if cfg.Prometheus.Job == "" {
		cfg.Prometheus.Job = "node"
	}
	if cfg.Devices == nil {
		cfg.Devices = make(DevicesConfig)
	}
	for instance, device := range cfg.Devices {
		if device.Priority < 0 || device.Priority > 255 {
			return nil, fmt.Errorf("devices.%s.priority は 0-255 の範囲で指定してください: %d", instance, device.Priority)
		}
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
