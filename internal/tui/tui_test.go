package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ysunote/grafana-light/internal/config"
	"github.com/ysunote/grafana-light/internal/monitor"
)

func TestRenderSnapshotEmpty(t *testing.T) {
	out := RenderSnapshot(nil, time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC), false)

	if !strings.Contains(out, "Waiting for first Prometheus snapshot") {
		t.Fatal("empty snapshot message is missing")
	}
}

func TestRenderSnapshotDevices(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 10, 0, time.UTC)
	devices := []monitor.DeviceStatus{
		{
			Name:    "server-a",
			Alive:   true,
			CPU:     45.5,
			RAM:     62.3,
			Updated: now.Add(-5 * time.Second),
		},
		{
			Name:    "server-b",
			Alive:   false,
			Updated: now.Add(-90 * time.Second),
		},
	}

	out := RenderSnapshot(devices, now, false)

	if !strings.Contains(out, "alive:1") {
		t.Fatal("alive count is missing")
	}
	if !strings.Contains(out, "dead:1") {
		t.Fatal("dead count is missing")
	}
	if !strings.Contains(out, "server-a") {
		t.Fatal("alive device row is missing")
	}
	if !strings.Contains(out, "45.5%") {
		t.Fatal("cpu value is missing")
	}
	if !strings.Contains(out, "62.3%") {
		t.Fatal("ram value is missing")
	}
	if !strings.Contains(out, "1:alive") {
		t.Fatal("1:alive status is missing")
	}
	if !strings.Contains(out, "0:dead") {
		t.Fatal("0:dead status is missing")
	}
	if !strings.Contains(out, "5s") {
		t.Fatal("age label 5s is missing")
	}
	if !strings.Contains(out, "1m") {
		t.Fatal("age label 1m is missing")
	}
}

func TestRenderSnapshotWithColor(t *testing.T) {
	now := time.Now()
	devices := []monitor.DeviceStatus{
		{Name: "test", Alive: true, CPU: 50, RAM: 50, Updated: now},
	}
	out := RenderSnapshot(devices, now, true)
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("ANSI codes are missing in color mode")
	}
}

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  int
	}{
		{"abc", 10, 10},
		{"abcdefghij", 5, 5},
		{"日本語テスト", 4, 4},
	}
	for _, tt := range tests {
		got := runeWidth(tt.in, tt.width)
		runes := []rune(got)
		if len(runes) != tt.want {
			t.Errorf("runeWidth(%q, %d) rune count = %d, want %d", tt.in, tt.width, len(runes), tt.want)
		}
	}
}

func TestBarColorThresholds(t *testing.T) {
	low := renderBar(true, 30, false)
	if !strings.Contains(low, "███·······") {
		t.Errorf("low bar unexpected: %s", low)
	}

	high := renderBar(true, 95, false)
	if !strings.Contains(high, "██████████") {
		t.Errorf("high bar unexpected: %s", high)
	}

	dead := renderBar(false, 0, false)
	if !strings.Contains(dead, "n/a") {
		t.Errorf("dead bar unexpected: %s", dead)
	}
}

func TestStatusRendering(t *testing.T) {
	alive := renderStatus(true, false)
	if alive != "1:alive" {
		t.Errorf("alive = %q, want 1:alive", alive)
	}
	dead := renderStatus(false, false)
	if dead != "0:dead" {
		t.Errorf("dead = %q, want 0:dead", dead)
	}
}

func TestDrawRenameWithNoDevicesDoesNotPanic(t *testing.T) {
	a := &app{
		mon:   monitor.New("http://example.invalid", "node", nil, time.Second),
		cfg:   &config.Config{},
		out:   &bytes.Buffer{},
		mode:  modeRename,
		msg:   "",
		dirty: true,
	}

	a.draw()

	if a.mode != modeNormal {
		t.Fatalf("mode = %v, want modeNormal", a.mode)
	}
	if !strings.Contains(a.msg, "Rename cancelled") {
		t.Fatalf("message = %q, want rename cancelled notice", a.msg)
	}
}
