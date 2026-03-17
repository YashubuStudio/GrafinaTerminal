package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

// DeviceStatus は各デバイスの現在の状態
type DeviceStatus struct {
	Instance string
	Name     string
	Alive    bool
	CPU      float64 // percent
	RAM      float64 // percent
	Updated  time.Time
}

// Monitor はPrometheusに定期ポーリングして全デバイス状態を保持する
type Monitor struct {
	promURL  string
	job      string
	interval time.Duration
	client   *http.Client

	mu      sync.RWMutex
	devices []DeviceStatus

	aliasMu sync.RWMutex
	aliases map[string]string

	// SSE購読者
	subMu       sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func New(promURL, job string, aliases map[string]string, interval time.Duration) *Monitor {
	a := make(map[string]string, len(aliases))
	for k, v := range aliases {
		a[k] = v
	}
	return &Monitor{
		promURL:     promURL,
		job:         job,
		aliases:     a,
		interval:    interval,
		client:      &http.Client{Timeout: 5 * time.Second},
		subscribers: make(map[chan struct{}]struct{}),
	}
}

// Run は定期的にPrometheusをポーリングする（ctx完了まで）
func (m *Monitor) Run(ctx context.Context) {
	if err := m.Refresh(ctx); err != nil {
		log.Printf("poll エラー: %v", err)
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Refresh(ctx); err != nil {
				log.Printf("poll エラー: %v", err)
			}
		}
	}
}

// Refresh は1回だけPrometheusをポーリングして状態を更新する
func (m *Monitor) Refresh(ctx context.Context) error {
	return m.poll(ctx)
}

// Devices は現在のデバイス状態のスナップショットを返す
func (m *Monitor) Devices() []DeviceStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DeviceStatus, len(m.devices))
	copy(out, m.devices)
	return out
}

// SetAlias はデバイスの表示名を変更する（スレッドセーフ）
func (m *Monitor) SetAlias(instance, name string) {
	m.aliasMu.Lock()
	if m.aliases == nil {
		m.aliases = make(map[string]string)
	}
	m.aliases[instance] = name
	m.aliasMu.Unlock()

	changed := false

	m.mu.Lock()
	for i := range m.devices {
		if m.devices[i].Instance != instance {
			continue
		}
		if m.devices[i].Name != name {
			m.devices[i].Name = name
			changed = true
		}
	}
	if changed {
		sort.Slice(m.devices, func(i, j int) bool {
			return m.devices[i].Name < m.devices[j].Name
		})
	}
	m.mu.Unlock()

	if changed {
		m.notify()
	}
}

// Subscribe はデバイス状態更新の通知チャネルを返す
func (m *Monitor) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	m.subMu.Lock()
	m.subscribers[ch] = struct{}{}
	m.subMu.Unlock()
	return ch
}

// Unsubscribe は購読を解除する
func (m *Monitor) Unsubscribe(ch chan struct{}) {
	m.subMu.Lock()
	delete(m.subscribers, ch)
	m.subMu.Unlock()
	close(ch)
}

func (m *Monitor) notify() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for ch := range m.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (m *Monitor) poll(ctx context.Context) error {
	type result struct {
		data map[string]float64
		err  error
	}

	upCh := make(chan result, 1)
	cpuCh := make(chan result, 1)
	ramCh := make(chan result, 1)

	jobFilter := fmt.Sprintf(`job="%s"`, m.job)

	go func() {
		d, err := m.instantQuery(ctx, fmt.Sprintf(`up{%s}`, jobFilter))
		upCh <- result{d, err}
	}()
	go func() {
		d, err := m.instantQuery(ctx,
			fmt.Sprintf(`100 - (avg by (instance) (rate(node_cpu_seconds_total{%s,mode="idle"}[1m])) * 100)`, jobFilter))
		cpuCh <- result{d, err}
	}()
	go func() {
		d, err := m.instantQuery(ctx,
			fmt.Sprintf(`(1 - node_memory_MemAvailable_bytes{%s} / node_memory_MemTotal_bytes{%s}) * 100`, jobFilter, jobFilter))
		ramCh <- result{d, err}
	}()

	upRes := <-upCh
	cpuRes := <-cpuCh
	ramRes := <-ramCh

	if upRes.err != nil {
		return fmt.Errorf("up クエリエラー: %w", upRes.err)
	}
	if cpuRes.err != nil {
		log.Printf("cpu クエリエラー: %v", cpuRes.err)
	}
	if ramRes.err != nil {
		log.Printf("ram クエリエラー: %v", ramRes.err)
	}

	// エイリアス読み取りをロック保護
	m.aliasMu.RLock()
	aliasSnapshot := make(map[string]string, len(m.aliases))
	for k, v := range m.aliases {
		aliasSnapshot[k] = v
	}
	m.aliasMu.RUnlock()

	now := time.Now()
	devices := make([]DeviceStatus, 0, len(upRes.data))

	for instance, upVal := range upRes.data {
		name := instance
		if alias, ok := aliasSnapshot[instance]; ok {
			name = alias
		}

		ds := DeviceStatus{
			Instance: instance,
			Name:     name,
			Alive:    upVal == 1,
			Updated:  now,
		}

		if cpuRes.err == nil {
			if v, ok := cpuRes.data[instance]; ok {
				ds.CPU = v
			}
		}
		if ramRes.err == nil {
			if v, ok := ramRes.data[instance]; ok {
				ds.RAM = v
			}
		}

		devices = append(devices, ds)
	}

	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Name < devices[j].Name
	})

	m.mu.Lock()
	m.devices = devices
	m.mu.Unlock()

	m.notify()
	return nil
}

// instantQuery は Prometheus instant query を実行し、instance→value のマップを返す
func (m *Monitor) instantQuery(ctx context.Context, query string) (map[string]float64, error) {
	params := url.Values{"query": {query}}
	reqURL := fmt.Sprintf("%s/api/v1/query?%s", m.promURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Prometheus接続エラー: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus HTTP %d", resp.StatusCode)
	}

	var pr promResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("レスポンス解析エラー: %w", err)
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("Prometheusエラー: %s", pr.Status)
	}

	result := make(map[string]float64, len(pr.Data.Result))
	for _, r := range pr.Data.Result {
		instance := r.Metric["instance"]
		if instance == "" {
			continue
		}
		if len(r.Value) < 2 {
			continue
		}
		vStr, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(vStr, 64)
		if err != nil {
			continue
		}
		result[instance] = v
	}

	return result, nil
}

type promResponse struct {
	Status string   `json:"status"`
	Data   promData `json:"data"`
}

type promData struct {
	ResultType string           `json:"resultType"`
	Result     []promVectorItem `json:"result"`
}

type promVectorItem struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}
