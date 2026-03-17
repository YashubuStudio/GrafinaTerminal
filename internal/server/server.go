package server

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/ysunote/grafana-light/internal/monitor"
)

type Server struct {
	mon *monitor.Monitor
}

func New(mon *monitor.Monitor) *Server {
	return &Server{mon: mon}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/events", s.handleSSE)
	mux.HandleFunc("/health", s.handleHealth)
}

// GET /health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

// GET / - メインページ
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	devices := monitor.SortedDevices(s.mon.Devices(), monitor.DefaultSortOptions())
	tbody := renderTableBody(devices)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pageTmpl.Execute(w, template.HTML(tbody))
}

// GET /events - SSEストリーム
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE未対応", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.mon.Subscribe()
	defer s.mon.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			devices := monitor.SortedDevices(s.mon.Devices(), monitor.DefaultSortOptions())
			tbody := renderTableBody(devices)
			// SSE data行: 改行を\nに分割して送る
			lines := strings.Split(tbody, "\n")
			for _, line := range lines {
				fmt.Fprintf(w, "data: %s\n", line)
			}
			fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}

func renderTableBody(devices []monitor.DeviceStatus) string {
	if len(devices) == 0 {
		return `<tr><td colspan="4" style="text-align:center;color:#6c7086;padding:2rem">デバイスが見つかりません</td></tr>`
	}

	var sb strings.Builder
	for _, d := range devices {
		// 死活: 1=alive(green), 0=dead(red)
		statusCode := "0"
		statusLabel := "dead"
		statusColor := "#f38ba8"
		if d.Alive {
			statusCode = "1"
			statusLabel = "alive"
			statusColor = "#a6e3a1"
		}

		cpuColor := barColor(d.CPU)
		ramColor := barColor(d.RAM)

		sb.WriteString(`<tr>`)

		// デバイス名
		sb.WriteString(fmt.Sprintf(`<td class="c-name">%s<span class="instance">%s</span></td>`,
			template.HTMLEscapeString(d.Name),
			template.HTMLEscapeString(d.Instance)))

		// CPU
		sb.WriteString(fmt.Sprintf(`<td class="c-metric"><div class="bar-bg"><div class="bar" style="width:%.1f%%;background:%s"></div></div><span>%.1f%%</span></td>`,
			d.CPU, cpuColor, d.CPU))

		// RAM
		sb.WriteString(fmt.Sprintf(`<td class="c-metric"><div class="bar-bg"><div class="bar" style="width:%.1f%%;background:%s"></div></div><span>%.1f%%</span></td>`,
			d.RAM, ramColor, d.RAM))

		// 死活
		sb.WriteString(fmt.Sprintf(`<td class="c-status"><span class="dot" style="background:%s"></span>%s:%s</td>`,
			statusColor, statusCode, statusLabel))

		sb.WriteString(`</tr>`)
	}
	return sb.String()
}

func barColor(v float64) string {
	if v > 90 {
		return "#f38ba8"
	}
	if v > 70 {
		return "#f9e2af"
	}
	return "#a6e3a1"
}

var pageTmpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>grafana-light</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{background:#11111b;color:#cdd6f4;font-family:system-ui,sans-serif}
header{padding:.6rem 1rem;border-bottom:1px solid #313244;display:flex;justify-content:space-between;align-items:center}
header b{font-size:1rem}
header .status{font-size:.75rem;color:#a6e3a1}
table{width:100%;border-collapse:collapse}
thead th{padding:.5rem .75rem;text-align:left;font-size:.75rem;font-weight:normal;color:#6c7086;border-bottom:2px solid #313244;text-transform:uppercase;letter-spacing:.05em}
tbody tr{border-bottom:1px solid #1e1e2e}
tbody tr:hover{background:#1e1e2e}
td{padding:.5rem .75rem;font-size:.85rem}
.c-name{min-width:140px}
.c-name .instance{display:block;font-size:.7rem;color:#6c7086}
.c-metric{min-width:160px}
.c-metric span{display:inline-block;width:3.5rem;text-align:right;font-size:.8rem;font-variant-numeric:tabular-nums}
.c-status{min-width:100px;font-weight:bold;font-variant-numeric:tabular-nums}
.bar-bg{display:inline-block;width:80px;height:10px;background:#313244;border-radius:5px;vertical-align:middle;margin-right:.4rem;overflow:hidden}
.bar{height:100%;border-radius:5px;transition:width .3s}
.dot{display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:.4rem;vertical-align:middle}
@media(max-width:600px){
  .bar-bg{width:50px}
  .c-name .instance{display:none}
  td{padding:.4rem .5rem;font-size:.8rem}
}
</style>
</head>
<body>
<header>
  <b>grafana-light</b>
  <span class="status" id="conn">connecting...</span>
</header>
<table>
<thead><tr><th>Device</th><th>CPU</th><th>RAM</th><th>Status</th></tr></thead>
<tbody id="devices">{{.}}</tbody>
</table>
<script>
var es=new EventSource("/events"),tb=document.getElementById("devices"),st=document.getElementById("conn");
es.onmessage=function(e){tb.innerHTML=e.data;st.textContent="live";st.style.color="#a6e3a1"};
es.onerror=function(){st.textContent="disconnected";st.style.color="#f38ba8"};
</script>
</body>
</html>`))
