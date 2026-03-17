package tui

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/ysunote/grafana-light/internal/config"
	"github.com/ysunote/grafana-light/internal/monitor"
)

const (
	clearScreen = "\x1b[H\x1b[2J"
	homeCursor  = "\x1b[H"
	clearToEnd  = "\x1b[J"
	clearLine   = "\x1b[2K"
	enterAlt    = "\x1b[?1049h"
	exitAlt     = "\x1b[?1049l"
	hideCursor  = "\x1b[?25l"
	showCursor  = "\x1b[?25h"
	reset       = "\x1b[0m"
	bold        = "\x1b[1m"
	dim         = "\x1b[2m"
	reverse     = "\x1b[7m"
	fgRed       = "\x1b[31m"
	fgGreen     = "\x1b[32m"
	fgYellow    = "\x1b[33m"
	fgCyan      = "\x1b[36m"
	fgGray      = "\x1b[90m"
)

type appMode int

const (
	modeNormal appMode = iota
	modeRename
	modePriority
	modeAdd
)

type app struct {
	mon     *monitor.Monitor
	cfg     *config.Config
	cfgPath string
	out     io.Writer

	selected        string
	mode            appMode
	editBuf         string
	dirty           bool
	msg             string
	msgTime         time.Time
	priorityAsc     bool
	metricSort      bool
	defaultTermSize int
	lastLines       []string
	lastWidth       int
}

type tableLayout struct {
	nameWidth    int
	showPriority bool
	showTemp     bool
	showNet      bool
}

func RunInteractive(ctx context.Context, mon *monitor.Monitor, cfg *config.Config, cfgPath string) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("tui mode requires a terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	a := &app{
		mon:             mon,
		cfg:             cfg,
		cfgPath:         cfgPath,
		out:             os.Stdout,
		defaultTermSize: 100,
	}

	fmt.Fprint(a.out, enterAlt+hideCursor+clearScreen)
	defer fmt.Fprint(a.out, showCursor+exitAlt+"\r\n")

	ch := mon.Subscribe()
	defer mon.Unsubscribe(ch)

	keys := make(chan keyEvent, 16)
	go readKeys(os.Stdin, keys)

	a.draw()

	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ch:
			if !ok {
				return nil
			}
			a.draw()
		case k := <-keys:
			if a.handleKey(k) {
				return nil
			}
			a.draw()
		}
	}
}

func (a *app) handleKey(k keyEvent) bool {
	switch a.mode {
	case modeNormal:
		return a.handleNormal(k)
	case modeRename:
		a.handleRename(k)
	case modePriority:
		a.handlePriority(k)
	case modeAdd:
		a.handleAdd(k)
	}
	return false
}

func (a *app) handleNormal(k keyEvent) bool {
	devices := a.sortedDevices()

	switch k.typ {
	case keyCtrlC:
		return true
	case keyUp:
		a.moveSelection(devices, -1)
	case keyDown:
		a.moveSelection(devices, 1)
	case keyChar:
		switch k.char {
		case 'q':
			return true
		case 'j':
			a.moveSelection(devices, 1)
		case 'k':
			a.moveSelection(devices, -1)
		case 'r':
			if d, ok := a.selectedDevice(devices); ok {
				a.mode = modeRename
				a.editBuf = d.Name
			}
		case 'p':
			if d, ok := a.selectedDevice(devices); ok {
				a.mode = modePriority
				a.editBuf = strconv.Itoa(d.Priority)
			}
		case 'a':
			a.mode = modeAdd
			a.editBuf = ""
		case 's':
			a.saveConfig()
		case 'o':
			a.priorityAsc = !a.priorityAsc
			a.setMsg("Order → " + a.sortLabel())
		case 'm':
			a.metricSort = !a.metricSort
			if a.metricSort {
				a.setMsg("Metric sort enabled")
			} else {
				a.setMsg("Metric sort disabled")
			}
		}
	}
	return false
}

func (a *app) handleRename(k keyEvent) {
	switch k.typ {
	case keyEsc, keyCtrlC:
		a.cancelEdit("Rename cancelled")
	case keyEnter:
		devices := a.sortedDevices()
		d, ok := a.selectedDevice(devices)
		name := strings.TrimSpace(a.editBuf)
		if ok && name != "" {
			a.ensureConfigDevices()
			a.cfg.Devices.Upsert(d.Instance, name, d.Priority)
			a.mon.SetDevice(d.Instance, monitor.DeviceConfig{Name: name, Priority: d.Priority})
			a.dirty = true
			a.setMsg("Renamed → " + name)
		}
		a.mode = modeNormal
		a.editBuf = ""
	case keyBackspace:
		a.popInput()
	case keyChar:
		a.editBuf += string(k.char)
	}
}

func (a *app) handlePriority(k keyEvent) {
	switch k.typ {
	case keyEsc, keyCtrlC:
		a.cancelEdit("Priority edit cancelled")
	case keyEnter:
		devices := a.sortedDevices()
		d, ok := a.selectedDevice(devices)
		if !ok {
			a.cancelEdit("Priority edit cancelled")
			return
		}

		priority, err := parsePriority(a.editBuf)
		if err != nil {
			a.setMsg(err.Error())
			return
		}

		name := a.configNameFor(d.Instance, d.Name)
		a.ensureConfigDevices()
		a.cfg.Devices.Upsert(d.Instance, name, priority)
		a.mon.SetDevice(d.Instance, monitor.DeviceConfig{Name: name, Priority: priority})
		a.dirty = true
		a.mode = modeNormal
		a.editBuf = ""
		a.setMsg(fmt.Sprintf("Priority → %d", priority))
	case keyBackspace:
		a.popInput()
	case keyChar:
		if k.char >= '0' && k.char <= '9' {
			a.editBuf += string(k.char)
		}
	}
}

func (a *app) handleAdd(k keyEvent) {
	switch k.typ {
	case keyEsc, keyCtrlC:
		a.cancelEdit("Add cancelled")
	case keyEnter:
		instance, name, priority, err := parseAddInput(a.editBuf)
		if err != nil {
			a.setMsg(err.Error())
			return
		}

		a.ensureConfigDevices()
		a.cfg.Devices.Upsert(instance, name, priority)
		a.mon.SetDevice(instance, monitor.DeviceConfig{Name: name, Priority: priority})
		a.selected = instance
		a.dirty = true
		a.mode = modeNormal
		a.editBuf = ""
		a.setMsg("Registered → " + instance)
	case keyBackspace:
		a.popInput()
	case keyChar:
		a.editBuf += string(k.char)
	}
}

func (a *app) ensureConfigDevices() {
	if a.cfg.Devices == nil {
		a.cfg.Devices = make(config.DevicesConfig)
	}
}

func (a *app) cancelEdit(msg string) {
	a.mode = modeNormal
	a.editBuf = ""
	a.setMsg(msg)
}

func (a *app) saveConfig() {
	if err := a.cfg.Save(a.cfgPath); err != nil {
		a.setMsg("Save failed: " + err.Error())
		return
	}
	a.dirty = false
	a.setMsg("Saved!")
}

func (a *app) setMsg(s string) {
	a.msg = s
	a.msgTime = time.Now()
}

func (a *app) popInput() {
	if len(a.editBuf) == 0 {
		return
	}
	runes := []rune(a.editBuf)
	a.editBuf = string(runes[:len(runes)-1])
}

func (a *app) sortedDevices() []monitor.DeviceStatus {
	mode := monitor.SortByPriority
	if a.metricSort {
		mode = monitor.SortByMetric
	}
	devices := monitor.SortedDevices(a.mon.Devices(), monitor.SortOptions{
		Mode:              mode,
		PriorityAscending: a.priorityAsc,
	})
	a.syncSelection(devices)
	return devices
}

func (a *app) syncSelection(devices []monitor.DeviceStatus) {
	if len(devices) == 0 {
		a.selected = ""
		return
	}
	if a.selected == "" {
		a.selected = devices[0].Instance
		return
	}
	for _, d := range devices {
		if d.Instance == a.selected {
			return
		}
	}
	a.selected = devices[0].Instance
}

func (a *app) selectedDevice(devices []monitor.DeviceStatus) (monitor.DeviceStatus, bool) {
	a.syncSelection(devices)
	for _, d := range devices {
		if d.Instance == a.selected {
			return d, true
		}
	}
	return monitor.DeviceStatus{}, false
}

func (a *app) currentIndex(devices []monitor.DeviceStatus) int {
	a.syncSelection(devices)
	for i, d := range devices {
		if d.Instance == a.selected {
			return i
		}
	}
	return 0
}

func (a *app) moveSelection(devices []monitor.DeviceStatus, delta int) {
	if len(devices) == 0 {
		a.selected = ""
		return
	}
	idx := a.currentIndex(devices) + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(devices) {
		idx = len(devices) - 1
	}
	a.selected = devices[idx].Instance
}

func (a *app) configNameFor(instance, displayName string) string {
	if cfg, ok := a.cfg.Devices[instance]; ok {
		return cfg.Name
	}
	if displayName == instance {
		return ""
	}
	return displayName
}

func (a *app) sortLabel() string {
	if a.metricSort {
		return "metric:max desc"
	}
	if a.priorityAsc {
		return "priority asc"
	}
	return "priority desc"
}

func (a *app) draw() {
	devices := a.sortedDevices()
	now := time.Now()

	if a.msg != "" && now.Sub(a.msgTime) > 3*time.Second {
		a.msg = ""
	}
	if len(devices) == 0 && (a.mode == modeRename || a.mode == modePriority) {
		a.mode = modeNormal
		a.editBuf = ""
		a.setMsg("Edit cancelled: no devices")
	}

	width := a.terminalWidth()
	layout := chooseLayout(width)
	selectedIdx := a.currentIndex(devices)

	var sb strings.Builder

	aliveN := 0
	for _, d := range devices {
		if d.Alive {
			aliveN++
		}
	}
	deadN := len(devices) - aliveN

	sb.WriteString(fmt.Sprintf("%s%sgrafana-light%s  %s\r\n", bold, fgCyan, reset, now.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("devices:%d  %salive:%d%s  %sdead:%d%s  %ssort:%s %s\r\n\r\n",
		len(devices), fgGreen, aliveN, reset, fgRed, deadN, reset, dim, reset, a.sortLabel()))

	sb.WriteString(renderHeader(layout, true))
	sb.WriteString(renderSeparator(width))

	if len(devices) == 0 {
		sb.WriteString(fmt.Sprintf("%sWaiting for first Prometheus snapshot...%s\r\n", fgGray, reset))
	} else {
		for i, d := range devices {
			sb.WriteString(renderRow(d, rowRenderOptions{
				color:      true,
				layout:     layout,
				now:        now,
				selected:   i == selectedIdx,
				mode:       a.mode,
				editBuf:    a.editBuf,
				showCursor: i == selectedIdx && (a.mode == modeRename || a.mode == modePriority),
			}))
		}
	}

	sb.WriteString("\r\n")
	sb.WriteString(a.statusLine(devices))
	if a.msg != "" {
		sb.WriteString(fmt.Sprintf("  %s%s%s", fgGreen, a.msg, reset))
	}
	sb.WriteString("\r\n")

	frame := strings.TrimSuffix(sb.String(), "\r\n")
	lines := strings.Split(frame, "\r\n")
	a.writeFrame(lines, width)
}

func (a *app) statusLine(devices []monitor.DeviceStatus) string {
	switch a.mode {
	case modeNormal:
		line := fmt.Sprintf("%s[j/k]%s move  %s[a]%s add  %s[r]%s rename  %s[p]%s priority  %s[o]%s asc/desc  %s[m]%s metric-sort  %s[s]%s save  %s[q]%s quit",
			dim, reset, dim, reset, dim, reset, dim, reset, dim, reset, dim, reset, dim, reset, dim, reset)
		if a.dirty {
			line += fmt.Sprintf("  %s● unsaved%s", fgYellow, reset)
		}
		return line
	case modeRename:
		if d, ok := a.selectedDevice(devices); ok {
			return fmt.Sprintf("rename %s%s%s →  %s[Enter]%s confirm  %s[Esc]%s cancel",
				fgGray, d.Instance, reset, dim, reset, dim, reset)
		}
	case modePriority:
		if d, ok := a.selectedDevice(devices); ok {
			return fmt.Sprintf("priority %s%s%s → 0-255  %s[Enter]%s confirm  %s[Esc]%s cancel",
				fgGray, d.Instance, reset, dim, reset, dim, reset)
		}
	case modeAdd:
		return fmt.Sprintf("add %sinstance,name,priority%s  %s[Enter]%s register  %s[Esc]%s cancel",
			fgGray, reset, dim, reset, dim, reset)
	}
	return ""
}

func (a *app) terminalWidth() int {
	if f, ok := a.out.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	if a.defaultTermSize > 0 {
		return a.defaultTermSize
	}
	return 100
}

type keyType int

const (
	keyChar keyType = iota
	keyUp
	keyDown
	keyEnter
	keyEsc
	keyBackspace
	keyCtrlC
)

type keyEvent struct {
	typ  keyType
	char rune
}

func readKeys(r io.Reader, ch chan<- keyEvent) {
	buf := make([]byte, 16)
	for {
		n, err := r.Read(buf)
		if err != nil || n == 0 {
			return
		}
		i := 0
		for i < n {
			b := buf[i]
			switch {
			case b == 3:
				ch <- keyEvent{typ: keyCtrlC}
				i++
			case b == 13 || b == 10:
				ch <- keyEvent{typ: keyEnter}
				i++
			case b == 27:
				if i+2 < n && buf[i+1] == '[' {
					switch buf[i+2] {
					case 'A':
						ch <- keyEvent{typ: keyUp}
					case 'B':
						ch <- keyEvent{typ: keyDown}
					}
					i += 3
				} else {
					ch <- keyEvent{typ: keyEsc}
					i++
				}
			case b == 127 || b == 8:
				ch <- keyEvent{typ: keyBackspace}
				i++
			default:
				r, size := utf8.DecodeRune(buf[i:n])
				if r != utf8.RuneError && b >= 32 {
					ch <- keyEvent{typ: keyChar, char: r}
				}
				i += size
			}
		}
	}
}

func RenderSnapshot(devices []monitor.DeviceStatus, now time.Time, color bool) string {
	layout := chooseLayout(140)
	c := colorizer(color)
	var sb strings.Builder

	alive := 0
	for _, d := range devices {
		if d.Alive {
			alive++
		}
	}
	dead := len(devices) - alive

	sb.WriteString(c(bold+fgCyan, "grafana-light"))
	sb.WriteString(fmt.Sprintf("  %s\n", now.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("devices:%d  ", len(devices)))
	sb.WriteString(c(fgGreen, fmt.Sprintf("alive:%d", alive)))
	sb.WriteString("  ")
	sb.WriteString(c(fgRed, fmt.Sprintf("dead:%d", dead)))
	sb.WriteString("\n\n")

	sb.WriteString(renderHeader(layout, color))
	sb.WriteString(renderSeparatorWithColor(99, color))

	if len(devices) == 0 {
		sb.WriteString(c(fgGray, "Waiting for first Prometheus snapshot...\n"))
		return sb.String()
	}

	for _, d := range devices {
		sb.WriteString(renderRow(d, rowRenderOptions{
			color:  color,
			layout: layout,
			now:    now,
		}))
	}

	return sb.String()
}

type rowRenderOptions struct {
	color      bool
	layout     tableLayout
	now        time.Time
	selected   bool
	mode       appMode
	editBuf    string
	showCursor bool
}

func renderHeader(layout tableLayout, color bool) string {
	c := colorizer(color)
	var sb strings.Builder
	sb.WriteString(c(dim, fmt.Sprintf("%-*s %-18s %-18s", layout.nameWidth+2, "DEVICE", "CPU", "RAM")))
	if layout.showPriority {
		sb.WriteString(" PRIO")
	}
	if layout.showTemp {
		sb.WriteString(" TEMP")
	}
	if layout.showNet {
		sb.WriteString(" RX         TX")
	}
	sb.WriteString(" STATUS     SEEN")
	if color {
		sb.WriteString(reset)
	}
	sb.WriteString("\r\n")
	return sb.String()
}

func renderSeparator(width int) string {
	return renderSeparatorWithColor(width, true)
}

func renderSeparatorWithColor(width int, color bool) string {
	if width < 60 {
		width = 60
	}
	c := colorizer(color)
	return c(dim, strings.Repeat("─", width-3)) + "\r\n"
}

func renderRow(d monitor.DeviceStatus, opts rowRenderOptions) string {
	var sb strings.Builder

	prefix := "  "
	name := runeWidth(d.Name, opts.layout.nameWidth)
	if opts.selected {
		prefix = fgCyan + "> " + reset
		switch opts.mode {
		case modeRename:
			name = fgYellow + editField(opts.editBuf, opts.layout.nameWidth, opts.showCursor) + reset
		default:
			name = reverse + runeWidth(d.Name, opts.layout.nameWidth) + reset
		}
	}

	sb.WriteString(prefix)
	sb.WriteString(fmt.Sprintf("%-*s %-18s %-18s",
		opts.layout.nameWidth, name,
		renderBar(d.Alive, d.CPU, opts.color),
		renderBar(d.Alive, d.RAM, opts.color)))

	if opts.layout.showPriority {
		priority := fmt.Sprintf("%3d", d.Priority)
		if opts.selected && opts.mode == modePriority {
			priority = editField(opts.editBuf, 3, opts.showCursor)
		}
		sb.WriteString(" " + padText(priority, 4))
	}
	if opts.layout.showTemp {
		sb.WriteString(" " + padText(renderTemp(d, opts.color), 5))
	}
	if opts.layout.showNet {
		sb.WriteString(" " + padText(renderRate(d.NetRxBps, d.HasNet, opts.color), 10))
		sb.WriteString(" " + padText(renderRate(d.NetTxBps, d.HasNet, opts.color), 10))
	}
	sb.WriteString(" " + padText(renderStatus(d.Alive, opts.color), 10))
	sb.WriteString(" " + padText(renderAge(opts.now, d.Updated, opts.color), 6))
	sb.WriteString("\r\n")
	return sb.String()
}

func chooseLayout(width int) tableLayout {
	layout := tableLayout{
		nameWidth:    24,
		showPriority: true,
		showTemp:     width >= 98,
		showNet:      width >= 122,
	}

	switch {
	case width < 88:
		layout.nameWidth = 18
		layout.showPriority = false
	case width < 98:
		layout.nameWidth = 20
	case width < 122:
		layout.nameWidth = 18
	default:
		layout.nameWidth = 20
	}

	return layout
}

func (a *app) writeFrame(lines []string, width int) {
	var sb strings.Builder

	if len(a.lastLines) == 0 || a.lastWidth != width {
		sb.WriteString(homeCursor)
		for _, line := range lines {
			sb.WriteString(line)
			sb.WriteString("\r\n")
		}
		sb.WriteString(clearToEnd)
		fmt.Fprint(a.out, sb.String())
		a.lastLines = append([]string(nil), lines...)
		a.lastWidth = width
		return
	}

	maxLines := len(lines)
	if len(a.lastLines) > maxLines {
		maxLines = len(a.lastLines)
	}

	for i := 0; i < maxLines; i++ {
		current := ""
		if i < len(lines) {
			current = lines[i]
		}
		previous := ""
		if i < len(a.lastLines) {
			previous = a.lastLines[i]
		}
		if current == previous {
			continue
		}

		sb.WriteString(fmt.Sprintf("\x1b[%d;1H", i+1))
		sb.WriteString(clearLine)
		sb.WriteString(current)
	}

	if len(lines) < len(a.lastLines) {
		sb.WriteString(fmt.Sprintf("\x1b[%d;1H", len(lines)+1))
		sb.WriteString(clearToEnd)
	}

	fmt.Fprint(a.out, sb.String())
	a.lastLines = append([]string(nil), lines...)
	a.lastWidth = width
}

func renderBar(alive bool, value float64, color bool) string {
	c := colorizer(color)
	if !alive {
		return c(fgGray, "[··········]   n/a")
	}
	value = clamp(value, 0, 100)
	filled := int(math.Round((value / 100) * 10))
	if filled > 10 {
		filled = 10
	}
	barStr := strings.Repeat("█", filled) + strings.Repeat("·", 10-filled)

	clr := fgGreen
	if value > 70 {
		clr = fgYellow
	}
	if value > 90 {
		clr = fgRed
	}

	return fmt.Sprintf("[%s] %5.1f%%", c(clr, barStr), value)
}

func renderTemp(d monitor.DeviceStatus, color bool) string {
	c := colorizer(color)
	if !d.HasTemp {
		return c(fgGray, "n/a")
	}
	text := fmt.Sprintf("%.1fC", d.TempC)
	switch {
	case d.TempC >= 80:
		return c(fgRed, text)
	case d.TempC >= 65:
		return c(fgYellow, text)
	default:
		return c(fgCyan, text)
	}
}

func renderRate(v float64, has bool, color bool) string {
	c := colorizer(color)
	if !has {
		return c(fgGray, "n/a")
	}
	return c(fgCyan, humanRate(v))
}

func humanRate(v float64) string {
	units := []string{"B/s", "K/s", "M/s", "G/s"}
	value := v
	idx := 0
	for value >= 1024 && idx < len(units)-1 {
		value /= 1024
		idx++
	}
	if idx == 0 {
		return fmt.Sprintf("%.0f%s", value, units[idx])
	}
	return fmt.Sprintf("%.1f%s", value, units[idx])
}

func renderStatus(alive bool, color bool) string {
	c := colorizer(color)
	if alive {
		return c(fgGreen, "1:alive")
	}
	return c(fgRed, "0:dead")
}

func renderAge(now, updated time.Time, color bool) string {
	c := colorizer(color)
	if updated.IsZero() {
		return c(fgGray, "never")
	}

	age := now.Sub(updated)
	if age < 0 {
		age = 0
	}

	var s string
	switch {
	case age < time.Minute:
		s = fmt.Sprintf("%ds", int(age/time.Second))
	case age < time.Hour:
		s = fmt.Sprintf("%dm", int(age/time.Minute))
	default:
		s = fmt.Sprintf("%dh", int(age/time.Hour))
	}

	if age > 30*time.Second {
		return c(fgYellow, s)
	}
	return c(fgGray, s)
}

func colorizer(color bool) func(string, string) string {
	if !color {
		return func(_, text string) string { return text }
	}
	return func(ansi, text string) string {
		return ansi + text + reset
	}
}

func runeWidth(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n <= width {
		return s + strings.Repeat(" ", width-n)
	}
	if width <= 3 {
		return string([]rune(s)[:width])
	}
	return string([]rune(s)[:width-3]) + "..."
}

func editField(s string, width int, showCursor bool) string {
	out := runeWidth(s, width)
	if showCursor && width > 0 {
		runes := []rune(out)
		runes[width-1] = '█'
		return string(runes)
	}
	return out
}

func padText(s string, width int) string {
	n := utf8.RuneCountInString(stripANSI(s))
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func parsePriority(s string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("priority は 0-255 で入力してください")
	}
	if value < 0 || value > 255 {
		return 0, fmt.Errorf("priority は 0-255 で入力してください")
	}
	return value, nil
}

func parseAddInput(s string) (string, string, int, error) {
	parts := strings.Split(s, ",")
	if len(parts) == 0 {
		return "", "", 0, fmt.Errorf("instance,name,priority 形式で入力してください")
	}

	instance := strings.TrimSpace(parts[0])
	if instance == "" {
		return "", "", 0, fmt.Errorf("instance を入力してください")
	}

	name := ""
	if len(parts) >= 2 {
		name = strings.TrimSpace(parts[1])
	}

	priority := 0
	if len(parts) >= 3 && strings.TrimSpace(parts[2]) != "" {
		value, err := parsePriority(parts[2])
		if err != nil {
			return "", "", 0, err
		}
		priority = value
	}

	return instance, name, priority, nil
}

func clamp(v, min, max float64) float64 {
	switch {
	case v < min:
		return min
	case v > max:
		return max
	default:
		return v
	}
}
