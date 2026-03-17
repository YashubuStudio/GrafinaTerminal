package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestPrometheus(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		var resp promResponse

		switch {
		case strings.Contains(query, "node_cpu_seconds_total"):
			resp = promResponse{
				Status: "success",
				Data: promData{
					ResultType: "vector",
					Result: []promVectorItem{
						{Metric: map[string]string{"instance": "10.0.0.1:9100"}, Value: []interface{}{1234567890.0, "45.5"}},
					},
				},
			}
		case strings.Contains(query, "node_memory"):
			resp = promResponse{
				Status: "success",
				Data: promData{
					ResultType: "vector",
					Result: []promVectorItem{
						{Metric: map[string]string{"instance": "10.0.0.1:9100"}, Value: []interface{}{1234567890.0, "62.3"}},
					},
				},
			}
		case strings.Contains(query, "up{"):
			resp = promResponse{
				Status: "success",
				Data: promData{
					ResultType: "vector",
					Result: []promVectorItem{
						{Metric: map[string]string{"instance": "10.0.0.1:9100"}, Value: []interface{}{1234567890.0, "1"}},
						{Metric: map[string]string{"instance": "10.0.0.2:9100"}, Value: []interface{}{1234567890.0, "0"}},
					},
				},
			}
		default:
			resp = promResponse{Status: "success", Data: promData{ResultType: "vector"}}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestMonitorPoll(t *testing.T) {
	ts := newTestPrometheus(t)
	defer ts.Close()

	aliases := map[string]string{
		"10.0.0.1:9100": "server-a",
	}

	mon := New(ts.URL, "node", aliases, 1*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := mon.Refresh(ctx); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	devices := mon.Devices()
	if len(devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(devices))
	}

	var alive, dead *DeviceStatus
	for i := range devices {
		if devices[i].Instance == "10.0.0.1:9100" {
			alive = &devices[i]
		}
		if devices[i].Instance == "10.0.0.2:9100" {
			dead = &devices[i]
		}
	}

	if alive == nil || dead == nil {
		t.Fatal("デバイスが見つからない")
	}

	if alive.Name != "server-a" {
		t.Errorf("name = %s, want server-a", alive.Name)
	}
	if !alive.Alive {
		t.Error("server-a should be alive")
	}
	if alive.CPU < 45 || alive.CPU > 46 {
		t.Errorf("cpu = %.1f, want ~45.5", alive.CPU)
	}
	if alive.RAM < 62 || alive.RAM > 63 {
		t.Errorf("ram = %.1f, want ~62.3", alive.RAM)
	}

	if dead.Alive {
		t.Error("10.0.0.2 should be dead")
	}
}

func TestMonitorSubscribe(t *testing.T) {
	ts := newTestPrometheus(t)
	defer ts.Close()

	mon := New(ts.URL, "node", nil, 100*time.Millisecond)

	ch := mon.Subscribe()
	defer mon.Unsubscribe(ch)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := mon.Refresh(ctx); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	select {
	case <-ch:
		// OK
	case <-time.After(1 * time.Second):
		t.Error("通知がタイムアウト")
	}
}

func TestMonitorSetAliasUpdatesDevicesImmediately(t *testing.T) {
	ts := newTestPrometheus(t)
	defer ts.Close()

	mon := New(ts.URL, "node", nil, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := mon.Refresh(ctx); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	ch := mon.Subscribe()
	defer mon.Unsubscribe(ch)

	mon.SetAlias("10.0.0.2:9100", "server-z")

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("alias update notification timed out")
	}

	var renamed *DeviceStatus
	for i := range mon.Devices() {
		device := mon.Devices()[i]
		if device.Instance == "10.0.0.2:9100" {
			renamed = &device
			break
		}
	}

	if renamed == nil {
		t.Fatal("renamed device not found")
	}
	if renamed.Name != "server-z" {
		t.Fatalf("name = %s, want server-z", renamed.Name)
	}
}

func TestMonitorRefreshError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	mon := New(ts.URL, "node", nil, 1*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := mon.Refresh(ctx); err == nil {
		t.Fatal("expected refresh error")
	}
}

func TestSetAlias(t *testing.T) {
	ts := newTestPrometheus(t)
	defer ts.Close()

	mon := New(ts.URL, "node", nil, 1*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 初回: エイリアスなし → instance名がそのまま
	if err := mon.Refresh(ctx); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	// SetAliasで名前変更
	mon.SetAlias("10.0.0.1:9100", "my-server")

	// 再取得: エイリアスが反映
	if err := mon.Refresh(ctx); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}

	devices := mon.Devices()
	for _, d := range devices {
		if d.Instance == "10.0.0.1:9100" {
			if d.Name != "my-server" {
				t.Errorf("name = %s, want my-server", d.Name)
			}
			return
		}
	}
	t.Fatal("device not found")
}
