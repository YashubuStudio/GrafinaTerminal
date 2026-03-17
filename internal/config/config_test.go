package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	yaml := `
server:
  port: 3000
  interval: 5s
prometheus:
  url: http://prom:9090
  job: mynode
devices:
  "10.0.0.1:9100": "server-a"
`
	f := writeTmp(t, yaml)
	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Server.Port != 3000 {
		t.Errorf("port = %d, want 3000", cfg.Server.Port)
	}
	if cfg.Server.Interval.Unwrap() != 5*time.Second {
		t.Errorf("interval = %v, want 5s", cfg.Server.Interval.Unwrap())
	}
	if cfg.Prometheus.URL != "http://prom:9090" {
		t.Errorf("prometheus url = %s", cfg.Prometheus.URL)
	}
	if cfg.Prometheus.Job != "mynode" {
		t.Errorf("job = %s, want mynode", cfg.Prometheus.Job)
	}
	if cfg.Devices["10.0.0.1:9100"] != "server-a" {
		t.Errorf("device alias not loaded")
	}
}

func TestLoad_Defaults(t *testing.T) {
	f := writeTmp(t, `{}`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Interval.Unwrap() != 3*time.Second {
		t.Errorf("default interval = %v, want 3s", cfg.Server.Interval.Unwrap())
	}
	if cfg.Prometheus.URL != "http://localhost:9090" {
		t.Errorf("default prom url = %s", cfg.Prometheus.URL)
	}
	if cfg.Prometheus.Job != "node" {
		t.Errorf("default job = %s", cfg.Prometheus.Job)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("存在しないファイルでエラーが返らない")
	}
}

func TestLoad_InvalidNegativeInterval(t *testing.T) {
	f := writeTmp(t, `
server:
  interval: -1s
`)

	_, err := Load(f)
	if err == nil {
		t.Fatal("負の interval でエラーが返らない")
	}
}

func TestSave_RoundTrip(t *testing.T) {
	// ロード
	f := writeTmp(t, `
server:
  port: 3000
  interval: 5s
prometheus:
  url: http://prom:9090
  job: mynode
devices:
  "10.0.0.1:9100": "server-a"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	// エイリアス変更して保存
	cfg.Devices["10.0.0.2:9100"] = "server-b"

	outPath := f + ".saved.yaml"
	defer os.Remove(outPath)

	if err := cfg.Save(outPath); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// リロードして検証
	cfg2, err := Load(outPath)
	if err != nil {
		t.Fatalf("Reload error: %v", err)
	}

	if cfg2.Server.Port != 3000 {
		t.Errorf("port = %d, want 3000", cfg2.Server.Port)
	}
	if cfg2.Server.Interval.Unwrap() != 5*time.Second {
		t.Errorf("interval = %v, want 5s", cfg2.Server.Interval.Unwrap())
	}
	if cfg2.Devices["10.0.0.1:9100"] != "server-a" {
		t.Error("server-a alias lost")
	}
	if cfg2.Devices["10.0.0.2:9100"] != "server-b" {
		t.Error("server-b alias not saved")
	}
}

func writeTmp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}
