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
	if !strings.Contains(html, "var(--ok)") {
		t.Error("ok色がない")
	}
	if !strings.Contains(html, "45.5%") {
		t.Error("CPU値がない")
	}

	// dead デバイス
	if !strings.Contains(html, "0:dead") {
		t.Error("dead表示がない")
	}
	if !strings.Contains(html, "var(--bad)") {
		t.Error("bad色がない")
	}
}

func TestBarColor(t *testing.T) {
	tests := []struct {
		val  float64
		want string
	}{
		{50, "var(--ok)"},
		{75, "var(--warn)"},
		{95, "var(--bad)"},
	}
	for _, tt := range tests {
		got := barColor(tt.val)
		if got != tt.want {
			t.Errorf("barColor(%v) = %s, want %s", tt.val, got, tt.want)
		}
	}
}

func TestNewPageData_IncludesProtectionSettings(t *testing.T) {
	data := newPageData("<tr></tr>", ScreenProtection{
		Enabled:            true,
		PixelShiftInterval: 45 * time.Second,
		PixelShiftStep:     2,
		IdleDimAfter:       90 * time.Second,
		IdleBrightness:     0.65,
	})

	if data.TBody != "<tr></tr>" {
		t.Fatalf("tbody = %q, want rendered html", data.TBody)
	}
	if !data.Protection.Enabled {
		t.Fatal("protection.enabled = false, want true")
	}
	if data.Protection.PixelShiftMS != 45000 {
		t.Fatalf("pixelShiftMs = %d, want 45000", data.Protection.PixelShiftMS)
	}
	if data.Protection.IdleDimMS != 90000 {
		t.Fatalf("idleDimMs = %d, want 90000", data.Protection.IdleDimMS)
	}
	if data.Protection.IdleBrightness != "0.65" {
		t.Fatalf("idleBrightness = %s, want 0.65", data.Protection.IdleBrightness)
	}
}
