package server

import (
	"strings"
	"testing"
	"time"

	"github.com/ysunote/grafana-light/internal/monitor"
)

func TestRenderTableBody_Empty(t *testing.T) {
	html := renderTableBody(nil)
	if !strings.Contains(html, "デバイスが見つかりません") {
		t.Error("空データのメッセージがない")
	}
}

func TestRenderTableBody_Devices(t *testing.T) {
	devices := []monitor.DeviceStatus{
		{Instance: "10.0.0.1:9100", Name: "server-a", Alive: true, CPU: 45.5, RAM: 62.3, Updated: time.Now()},
		{Instance: "10.0.0.2:9100", Name: "10.0.0.2:9100", Alive: false, CPU: 0, RAM: 0, Updated: time.Now()},
	}

	html := renderTableBody(devices)

	// alive デバイス
	if !strings.Contains(html, "server-a") {
		t.Error("server-a の名前がない")
	}
	if !strings.Contains(html, "1:alive") {
		t.Error("alive表示がない")
	}
	if !strings.Contains(html, "#a6e3a1") {
		t.Error("green色がない")
	}
	if !strings.Contains(html, "45.5%") {
		t.Error("CPU値がない")
	}

	// dead デバイス
	if !strings.Contains(html, "0:dead") {
		t.Error("dead表示がない")
	}
	if !strings.Contains(html, "#f38ba8") {
		t.Error("red色がない")
	}
}

func TestBarColor(t *testing.T) {
	tests := []struct {
		val  float64
		want string
	}{
		{50, "#a6e3a1"},
		{75, "#f9e2af"},
		{95, "#f38ba8"},
	}
	for _, tt := range tests {
		got := barColor(tt.val)
		if got != tt.want {
			t.Errorf("barColor(%v) = %s, want %s", tt.val, got, tt.want)
		}
	}
}
