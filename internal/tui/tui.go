package tui

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/ysunote/grafana-light/internal/config"
	"github.com/ysunote/grafana-light/internal/monitor"
)

// ANSI escape sequences
const (
	clearScreen = "\x1b[H\x1b[2J"
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

// ─────────────────────────────────────────────
// Interactive TUI
// ─────────────────────────────────────────────

type appMode int

const (
	modeNormal appMode = iota
	modeRename
)

type app struct {
	mon     *monitor.Monitor
	cfg     *config.Config
	cfgPath string
	out     io.Writer

	cursor  int
	mode    appMode
	editBuf string
	dirty   bool
	msg     string
	msgTime time.Time
}

// RunInteractive は対話型TUIを起動する
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
		mon:     mon,
		cfg:     cfg,
		cfgPath: cfgPath,
		out:     os.Stdout,
	}

	fmt.Fprint(a.out, hideCursor)
	defer fmt.Fprint(a.out, showCursor+"\r\n")

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

func (a *app) handleKey(k keyEvent) (quit bool) {
	switch a.mode {
	case modeNormal:
		return a.handleNormal(k)
	case modeRename:
		a.handleRename(k)
	}
	return false
}

func (a *app) handleNormal(k keyEvent) bool {
	devices := a.mon.Devices()
	maxIdx := len(devices) - 1

	switch k.typ {
	case keyCtrlC:
		return true
	case keyUp:
		if a.cursor > 0 {
			a.cursor--
		}
	case keyDown:
		if a.cursor < maxIdx {
			a.cursor++
		}
	case keyChar:
		switch k.char {
		case 'q':
			return true
		case 'j':
			if a.cursor < maxIdx {
				a.cursor++
			}
		case 'k':
			if a.cursor > 0 {
				a.cursor--
			}
		case 'r':
			if len(devices) > 0 {
				a.mode = modeRename
				a.editBuf = devices[a.clampCursor(devices)].Name
			}
		case 's':
			a.saveConfig()
		}
	}
	return false
}

func (a *app) handleRename(k keyEvent) {
	switch k.typ {
	case keyEsc, keyCtrlC:
		a.mode = modeNormal
		a.editBuf = ""
		a.setMsg("Cancelled")
	case keyEnter:
		devices := a.mon.Devices()
		idx := a.clampCursor(devices)
		if idx < len(devices) && a.editBuf != "" {
			d := devices[idx]
			a.mon.SetAlias(d.Instance, a.editBuf)
			a.cfg.Devices[d.Instance] = a.editBuf
			a.dirty = true
			a.setMsg("Renamed → " + a.editBuf)
		}
		a.mode = modeNormal
		a.editBuf = ""
	case keyBackspace:
		if len(a.editBuf) > 0 {
			runes := []rune(a.editBuf)
			a.editBuf = string(runes[:len(runes)-1])
		}
	case keyChar:
		a.editBuf += string(k.char)
	}
}

func (a *app) saveConfig() {
	if err := a.cfg.Save(a.cfgPath); err != nil {
		a.setMsg("Save failed: " + err.Error())
	} else {
		a.dirty = false
		a.setMsg("Saved!")
	}
}

func (a *app) setMsg(s string) {
	a.msg = s
	a.msgTime = time.Now()
}

func (a *app) clampCursor(devices []monitor.DeviceStatus) int {
	if a.cursor >= len(devices) {
		a.cursor = len(devices) - 1
	}
	if a.cursor < 0 {
		a.cursor = 0
	}
	return a.cursor
}

func (a *app) draw() {
	devices := a.mon.Devices()
	now := time.Now()

	if a.msg != "" && now.Sub(a.msgTime) > 3*time.Second {
		a.msg = ""
	}
	a.clampCursor(devices)
	if len(devices) == 0 && a.mode == modeRename {
		a.mode = modeNormal
		a.editBuf = ""
		a.setMsg("Rename cancelled: no devices")
	}

	var sb strings.Builder
	sb.WriteString(clearScreen)

	// ヘッダー
	aliveN := 0
	for _, d := range devices {
		if d.Alive {
			aliveN++
		}
	}
	deadN := len(devices) - aliveN

	sb.WriteString(fmt.Sprintf("%s%sgrafana-light%s  %s\r\n", bold, fgCyan, reset, now.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("devices:%d  %salive:%d%s  %sdead:%d%s\r\n\r\n",
		len(devices), fgGreen, aliveN, reset, fgRed, deadN, reset))

	// カラム見出し
	sb.WriteString(fmt.Sprintf("%s   %-24s %-18s %-18s %-10s %-6s%s\r\n",
		dim, "DEVICE", "CPU", "RAM", "STATUS", "SEEN", reset))
	sb.WriteString(fmt.Sprintf("%s%s%s\r\n", dim, strings.Repeat("─", 83), reset))

	if len(devices) == 0 {
		sb.WriteString(fmt.Sprintf("%sWaiting for first Prometheus snapshot...%s\r\n", fgGray, reset))
	} else {
		for i, d := range devices {
			prefix := "  "
			name := runeWidth(d.Name, 24)

			if i == a.cursor {
				prefix = fgCyan + "> " + reset
				if a.mode == modeRename {
					name = fgYellow + runeWidth(a.editBuf, 23) + "█" + reset
				} else {
					name = reverse + runeWidth(d.Name, 24) + reset
				}
			}

			sb.WriteString(fmt.Sprintf("%s%-24s %-18s %-18s %-10s %-6s\r\n",
				prefix, name,
				renderBar(d.Alive, d.CPU, true),
				renderBar(d.Alive, d.RAM, true),
				renderStatus(d.Alive, true),
				renderAge(now, d.Updated, true)))
		}
	}

	sb.WriteString("\r\n")

	// ステータスバー
	switch a.mode {
	case modeNormal:
		sb.WriteString(fmt.Sprintf("%s[j/k]%s move  %s[r]%s rename  %s[s]%s save  %s[q]%s quit",
			dim, reset, dim, reset, dim, reset, dim, reset))
		if a.dirty {
			sb.WriteString(fmt.Sprintf("  %s● unsaved%s", fgYellow, reset))
		}
	case modeRename:
		if len(devices) == 0 {
			sb.WriteString(fmt.Sprintf("%s[r]%s rename unavailable", dim, reset))
		} else {
			d := devices[a.cursor]
			sb.WriteString(fmt.Sprintf("rename %s%s%s →  %s[Enter]%s confirm  %s[Esc]%s cancel",
				fgGray, d.Instance, reset, dim, reset, dim, reset))
		}
	}

	if a.msg != "" {
		sb.WriteString(fmt.Sprintf("  %s%s%s", fgGreen, a.msg, reset))
	}

	sb.WriteString("\r\n")
	fmt.Fprint(a.out, sb.String())
}

// ─────────────────────────────────────────────
// Key input
// ─────────────────────────────────────────────

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

// ─────────────────────────────────────────────
// Non-interactive rendering (for once mode)
// ─────────────────────────────────────────────

// RenderSnapshot はCLI向けの表示を返す（color=falseでANSI色なし）
func RenderSnapshot(devices []monitor.DeviceStatus, now time.Time, color bool) string {
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

	sb.WriteString(c(dim, fmt.Sprintf("%-24s %-18s %-18s %-10s %-6s\n",
		"DEVICE", "CPU", "RAM", "STATUS", "SEEN")))
	sb.WriteString(c(dim, strings.Repeat("─", 78)+"\n"))

	if len(devices) == 0 {
		sb.WriteString(c(fgGray, "Waiting for first Prometheus snapshot...\n"))
		return sb.String()
	}

	for _, d := range devices {
		sb.WriteString(fmt.Sprintf("%-24s %-18s %-18s %-10s %-6s\n",
			runeWidth(d.Name, 24),
			renderBar(d.Alive, d.CPU, color),
			renderBar(d.Alive, d.RAM, color),
			renderStatus(d.Alive, color),
			renderAge(now, d.Updated, color)))
	}

	return sb.String()
}

// ─────────────────────────────────────────────
// Shared rendering helpers
// ─────────────────────────────────────────────

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
