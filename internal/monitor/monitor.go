package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
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
	Priority int
	Alive    bool
	CPU      float64 // percent
	RAM      float64 // percent
	TempC    float64
	HasTemp  bool
	NetRxBps float64
	NetTxBps float64
	HasNet   bool
	Updated  time.Time
}

type DeviceConfig struct {
	Name     string
	Priority int
}

type SortMode int

const (
	SortByPriority SortMode = iota
	SortByMetric
)

type SortOptions struct {
	Mode              SortMode
	PriorityAscending bool
}

// Monitor はPrometheusに定期ポーリングして全デバイス状態を保持する
type Monitor struct {
	promURL  string
	job      string
	interval time.Duration
	client   *http.Client

	mu      sync.RWMutex
	devices []DeviceStatus

	configMu sync.RWMutex
	configs  map[string]DeviceConfig

	// SSE購読者
	subMu       sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func New(promURL, job string, devices map[string]DeviceConfig, interval time.Duration) *Monitor {
	cfg := make(map[string]DeviceConfig, len(devices))
	for k, v := range devices {
		cfg[k] = normalizeDeviceConfig(v)
	}
	return &Monitor{
		promURL:     promURL,
		job:         job,
		configs:     cfg,
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
	m.configMu.RLock()
	cfg := normalizeDeviceConfig(m.configs[instance])
	m.configMu.RUnlock()
	cfg.Name = name
	m.SetDevice(instance, cfg)
}

func (m *Monitor) SetPriority(instance string, priority int) {
	m.configMu.RLock()
	cfg := normalizeDeviceConfig(m.configs[instance])
	m.configMu.RUnlock()
	cfg.Priority = priority
	m.SetDevice(instance, cfg)
}

func (m *Monitor) SetDevice(instance string, cfg DeviceConfig) {
	cfg = normalizeDeviceConfig(cfg)

	m.configMu.Lock()
	if m.configs == nil {
		m.configs = make(map[string]DeviceConfig)
	}
	m.configs[instance] = cfg
	m.configMu.Unlock()

	changed := false
	displayName := deviceDisplayName(instance, cfg)
	m.mu.Lock()
	found := false
	for i := range m.devices {
		if m.devices[i].Instance != instance {
			continue
		}
		found = true
		if m.devices[i].Name != displayName || m.devices[i].Priority != cfg.Priority {
			m.devices[i].Name = displayName
			m.devices[i].Priority = cfg.Priority
			changed = true
		}
	}
	if !found {
		m.devices = append(m.devices, DeviceStatus{
			Instance: instance,
			Name:     displayName,
			Priority: cfg.Priority,
		})
		changed = true
	}
	if changed {
		sort.Slice(m.devices, func(i, j int) bool {
			return m.devices[i].Instance < m.devices[j].Instance
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
	tempCh := make(chan result, 1)
	rxCh := make(chan result, 1)
	txCh := make(chan result, 1)

	jobFilter := fmt.Sprintf(`job="%s"`, m.job)
	netFilter := `device!~"lo|docker.*|veth.*|br.*|virbr.*|cali.*|tun.*|tap.*"`

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
	go func() {
		d, err := m.instantQuery(ctx,
			fmt.Sprintf(`max by (instance) ((node_hwmon_temp_celsius{%s}) or (node_thermal_zone_temp{%s} / 1000))`, jobFilter, jobFilter))
		tempCh <- result{d, err}
	}()
	go func() {
		d, err := m.instantQuery(ctx,
			fmt.Sprintf(`sum by (instance) (rate(node_network_receive_bytes_total{%s,%s}[1m]))`, jobFilter, netFilter))
		rxCh <- result{d, err}
	}()
	go func() {
		d, err := m.instantQuery(ctx,
			fmt.Sprintf(`sum by (instance) (rate(node_network_transmit_bytes_total{%s,%s}[1m]))`, jobFilter, netFilter))
		txCh <- result{d, err}
	}()

	upRes := <-upCh
	cpuRes := <-cpuCh
	ramRes := <-ramCh
	tempRes := <-tempCh
	rxRes := <-rxCh
	txRes := <-txCh

	if upRes.err != nil {
		return fmt.Errorf("up クエリエラー: %w", upRes.err)
	}
	if cpuRes.err != nil {
		log.Printf("cpu クエリエラー: %v", cpuRes.err)
	}
	if ramRes.err != nil {
		log.Printf("ram クエリエラー: %v", ramRes.err)
	}
	if tempRes.err != nil {
		log.Printf("temp クエリエラー: %v", tempRes.err)
	}
	if rxRes.err != nil {
		log.Printf("rx クエリエラー: %v", rxRes.err)
	}
	if txRes.err != nil {
		log.Printf("tx クエリエラー: %v", txRes.err)
	}

	m.configMu.RLock()
	configSnapshot := make(map[string]DeviceConfig, len(m.configs))
	for k, v := range m.configs {
		configSnapshot[k] = normalizeDeviceConfig(v)
	}
	m.configMu.RUnlock()

	instanceSet := make(map[string]struct{}, len(configSnapshot)+len(upRes.data))
	for instance := range configSnapshot {
		instanceSet[instance] = struct{}{}
	}
	addResultInstances(instanceSet, upRes.data)
	addResultInstances(instanceSet, cpuRes.data)
	addResultInstances(instanceSet, ramRes.data)
	addResultInstances(instanceSet, tempRes.data)
	addResultInstances(instanceSet, rxRes.data)
	addResultInstances(instanceSet, txRes.data)

	instances := make([]string, 0, len(instanceSet))
	for instance := range instanceSet {
		instances = append(instances, instance)
	}
	sort.Strings(instances)

	now := time.Now()
	devices := make([]DeviceStatus, 0, len(instances))

	for _, instance := range instances {
		cfg := normalizeDeviceConfig(configSnapshot[instance])
		ds := DeviceStatus{
			Instance: instance,
			Name:     deviceDisplayName(instance, cfg),
			Priority: cfg.Priority,
		}

		if upVal, ok := upRes.data[instance]; ok {
			ds.Alive = upVal == 1
			ds.Updated = now
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
		if tempRes.err == nil {
			if v, ok := tempRes.data[instance]; ok {
				ds.TempC = v
				ds.HasTemp = true
			}
		}
		if rxRes.err == nil {
			if v, ok := rxRes.data[instance]; ok {
				ds.NetRxBps = v
				ds.HasNet = true
			}
		}
		if txRes.err == nil {
			if v, ok := txRes.data[instance]; ok {
				ds.NetTxBps = v
				ds.HasNet = true
			}
		}

		devices = append(devices, ds)
	}

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

func SortedDevices(devices []DeviceStatus, opts SortOptions) []DeviceStatus {
	out := make([]DeviceStatus, len(devices))
	copy(out, devices)
	sortDevices(out, opts)
	return out
}

func DefaultSortOptions() SortOptions {
	return SortOptions{Mode: SortByPriority}
}

func sortDevices(devices []DeviceStatus, opts SortOptions) {
	if len(devices) == 0 {
		return
	}

	if opts.Mode == SortByMetric {
		maxNet := 0.0
		for _, d := range devices {
			maxNet = math.Max(maxNet, d.NetRxBps)
			maxNet = math.Max(maxNet, d.NetTxBps)
		}
		score := make(map[string]float64, len(devices))
		for _, d := range devices {
			score[d.Instance] = metricScore(d, maxNet)
		}
		sort.SliceStable(devices, func(i, j int) bool {
			if score[devices[i].Instance] != score[devices[j].Instance] {
				return score[devices[i].Instance] > score[devices[j].Instance]
			}
			return comparePriority(devices[i], devices[j], opts.PriorityAscending)
		})
		return
	}

	sort.SliceStable(devices, func(i, j int) bool {
		return comparePriority(devices[i], devices[j], opts.PriorityAscending)
	})
}

func comparePriority(a, b DeviceStatus, ascending bool) bool {
	if a.Priority != b.Priority {
		if ascending {
			return a.Priority < b.Priority
		}
		return a.Priority > b.Priority
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.Instance < b.Instance
}

func metricScore(d DeviceStatus, maxNet float64) float64 {
	score := math.Max(d.CPU, d.RAM)
	if d.HasTemp {
		score = math.Max(score, clampFloat(d.TempC, 0, 100))
	}
	if d.HasNet && maxNet > 0 {
		score = math.Max(score, clampFloat((d.NetRxBps/maxNet)*100, 0, 100))
		score = math.Max(score, clampFloat((d.NetTxBps/maxNet)*100, 0, 100))
	}
	return score
}

func addResultInstances(set map[string]struct{}, data map[string]float64) {
	for instance := range data {
		set[instance] = struct{}{}
	}
}

func normalizeDeviceConfig(cfg DeviceConfig) DeviceConfig {
	if cfg.Priority < 0 {
		cfg.Priority = 0
	}
	if cfg.Priority > 255 {
		cfg.Priority = 255
	}
	return cfg
}

func deviceDisplayName(instance string, cfg DeviceConfig) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	return instance
}

func clampFloat(v, min, max float64) float64 {
	switch {
	case v < min:
		return min
	case v > max:
		return max
	default:
		return v
	}
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
