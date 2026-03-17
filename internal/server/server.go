package server

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/ysunote/grafana-light/internal/monitor"
)

type Server struct {
	mon        *monitor.Monitor
	protection ScreenProtection
}

type ScreenProtection struct {
	Enabled            bool
	PixelShiftInterval time.Duration
	PixelShiftStep     int
	IdleDimAfter       time.Duration
	IdleBrightness     float64
}

func New(mon *monitor.Monitor, protection ScreenProtection) *Server {
	return &Server{mon: mon, protection: protection}
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
	data := newPageData(tbody, s.protection)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pageTmpl.Execute(w, data)
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
		return `<tr><td colspan="4" style="text-align:center;color:var(--muted);padding:2rem">デバイスが見つかりません</td></tr>`
	}

	var sb strings.Builder
	for _, d := range devices {
		// 死活: 1=alive(green), 0=dead(red)
		statusCode := "0"
		statusLabel := "dead"
		statusColor := "var(--bad)"
		if d.Alive {
			statusCode = "1"
			statusLabel = "alive"
			statusColor = "var(--ok)"
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
		return "var(--bad)"
	}
	if v > 70 {
		return "var(--warn)"
	}
	return "var(--ok)"
}

type pageData struct {
	TBody      template.HTML
	Protection protectionViewData
}

type protectionViewData struct {
	Enabled        bool
	PixelShiftMS   int64
	PixelShiftStep int
	IdleDimMS      int64
	IdleBrightness string
}

func newPageData(tbody string, protection ScreenProtection) pageData {
	return pageData{
		TBody: template.HTML(tbody),
		Protection: protectionViewData{
			Enabled:        protection.Enabled,
			PixelShiftMS:   protection.PixelShiftInterval.Milliseconds(),
			PixelShiftStep: protection.PixelShiftStep,
			IdleDimMS:      protection.IdleDimAfter.Milliseconds(),
			IdleBrightness: fmt.Sprintf("%.2f", protection.IdleBrightness),
		},
	}
}

var pageTmpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>grafana-light</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
:root{
  --bg:#11111b;
  --panel:#11111b;
  --panel-accent:#1e1e2e;
  --line:#313244;
  --line-soft:#1e1e2e;
  --text:#cdd6f4;
  --muted:#6c7086;
  --ok:#a6e3a1;
  --warn:#f9e2af;
  --bad:#f38ba8;
  --bar-bg:#313244;
}
body.protected{
  --bg:#0d1115;
  --panel:#11161b;
  --panel-accent:#151c23;
  --line:#25303a;
  --line-soft:#1a222b;
  --text:#b7c1cc;
  --muted:#6f7b86;
  --ok:#84b689;
  --warn:#c8b073;
  --bad:#c7818a;
  --bar-bg:#24303a;
}
body{background:var(--bg);color:var(--text);font-family:system-ui,sans-serif;overflow:hidden}
main{min-height:100vh;transition:transform .9s ease,filter .35s ease,opacity .35s ease;will-change:transform,filter}
body.protected main{padding:.35rem}
body.protected main.is-idle{filter:brightness(var(--idle-brightness)) saturate(.88);opacity:.92}
header{padding:.6rem 1rem;border-bottom:1px solid var(--line);display:flex;justify-content:space-between;align-items:center;background:var(--panel)}
header b{font-size:1rem}
header .status{font-size:.75rem;color:var(--muted)}
table{width:100%;border-collapse:collapse;background:var(--panel)}
thead th{padding:.5rem .75rem;text-align:left;font-size:.75rem;font-weight:normal;color:var(--muted);border-bottom:1px solid var(--line);text-transform:uppercase;letter-spacing:.05em}
tbody tr{border-bottom:1px solid var(--line-soft)}
tbody tr:hover{background:var(--panel-accent)}
td{padding:.5rem .75rem;font-size:.85rem}
.c-name{min-width:140px}
.c-name .instance{display:block;font-size:.7rem;color:var(--muted)}
.c-metric{min-width:160px}
.c-metric span{display:inline-block;width:3.5rem;text-align:right;font-size:.8rem;font-variant-numeric:tabular-nums}
.c-status{min-width:100px;font-weight:bold;font-variant-numeric:tabular-nums}
.bar-bg{display:inline-block;width:80px;height:10px;background:var(--bar-bg);border-radius:5px;vertical-align:middle;margin-right:.4rem;overflow:hidden}
.bar{height:100%;border-radius:5px;transition:width .3s}
.dot{display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:.4rem;vertical-align:middle}
@media(max-width:600px){
  .bar-bg{width:50px}
  .c-name .instance{display:none}
  td{padding:.4rem .5rem;font-size:.8rem}
}
</style>
</head>
<body class="{{if .Protection.Enabled}}protected{{end}}" style="--idle-brightness: {{.Protection.IdleBrightness}};">
<main id="frame">
<header>
  <b>grafana-light</b>
  <span class="status" id="conn">connecting...</span>
</header>
<table>
<thead><tr><th>Device</th><th>CPU</th><th>RAM</th><th>Status</th></tr></thead>
<tbody id="devices">{{.TBody}}</tbody>
</table>
</main>
<script>
(function(){
var es=new EventSource("/events"),tb=document.getElementById("devices"),st=document.getElementById("conn");
es.onmessage=function(e){tb.innerHTML=e.data;st.textContent="live";st.style.color="var(--ok)"};
es.onerror=function(){st.textContent="disconnected";st.style.color="var(--bad)"};

var protection={enabled:{{if .Protection.Enabled}}true{{else}}false{{end}},pixelShiftMs:{{.Protection.PixelShiftMS}},pixelShiftStep:{{.Protection.PixelShiftStep}},idleDimMs:{{.Protection.IdleDimMS}}};
if(!protection.enabled){return;}

var frame=document.getElementById("frame");
var idleTimer=0;

function setIdle(isIdle){
  frame.classList.toggle("is-idle",isIdle);
}

function resetIdle(){
  setIdle(false);
  window.clearTimeout(idleTimer);
  if(protection.idleDimMs>0){
    idleTimer=window.setTimeout(function(){setIdle(true)},protection.idleDimMs);
  }
}

if(protection.pixelShiftStep>0&&protection.pixelShiftMs>0){
  var step=protection.pixelShiftStep;
  var offsets=[[0,0],[step,0],[step,step],[0,step],[-step,step],[-step,0],[-step,-step],[0,-step],[step,-step]];
  var offsetIndex=0;
  var applyShift=function(){
    var offset=offsets[offsetIndex%offsets.length];
    frame.style.transform="translate("+offset[0]+"px,"+offset[1]+"px)";
    offsetIndex++;
  };
  applyShift();
  window.setInterval(applyShift,protection.pixelShiftMs);
}

["mousemove","mousedown","keydown","touchstart"].forEach(function(eventName){
  window.addEventListener(eventName,resetIdle,{passive:true});
});

document.addEventListener("visibilitychange",function(){
  if(document.hidden){
    setIdle(true);
    return;
  }
  resetIdle();
});

resetIdle();
})();
</script>
</body>
</html>`))
