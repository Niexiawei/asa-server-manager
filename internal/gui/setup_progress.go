//go:build windows

package gui

import (
	"asa-server/internal/actions"
	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/logger"
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// steamProgressRe matches SteamCMD's "progress: 42.34" download lines so the
// panel's progress bar can show a real percentage instead of only a spinner
// (docs/SETUP_FLOW_OPTIMIZATION_PLAN.md S11).
var steamProgressRe = regexp.MustCompile(`progress:\s*([0-9]+(?:\.[0-9]+)?)`)

// guiProgressWriter turns the byte stream from installer.* into per-line
// callbacks. It does no UI work itself — onLine/onPct are supplied by the
// panel and each marshals onto the Fyne main thread via fyne.Do, so the
// install goroutine never touches a widget directly (CLAUDE.md GUI rule).
type guiProgressWriter struct {
	mu     sync.Mutex
	onLine func(line string)
	onPct  func(fraction float64)
}

func (w *guiProgressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, raw := range strings.Split(strings.ReplaceAll(string(p), "\r", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if w.onLine != nil {
			w.onLine(line)
		}
		if w.onPct != nil {
			if m := steamProgressRe.FindStringSubmatch(line); m != nil {
				if v, err := strconv.ParseFloat(m[1], 64); err == nil && v >= 0 && v <= 100 {
					w.onPct(v / 100)
				}
			}
		}
	}
	return len(p), nil
}

// showSetupProgress opens a window that runs actions.InstallBaseEnvironment
// (SteamCMD + ARK server files + first-run config) with a live log and a
// progress bar. Windows-only and, on Windows, the whole environment-init
// story for double-click users — there is no Wine/Proton preflight or
// runtime download here. See docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.7.
func (g *GUIApp) showSetupProgress() {
	w := g.app.NewWindow("环境初始化")
	w.Resize(fyne.NewSize(700, 480))

	stepLabel := widget.NewLabel("准备中...")
	stepLabel.TextStyle = fyne.TextStyle{Bold: true}

	bar := widget.NewProgressBar()
	barInf := widget.NewProgressBarInfinite()
	bar.Hide()

	logView := widget.NewMultiLineEntry()
	logView.Wrapping = fyne.TextWrapWord
	logView.SetMinRowsVisible(16)

	var (
		linesMu sync.Mutex
		lines   []string
	)
	const maxLines = 500
	appendLine := func(s string) {
		fyne.Do(func() {
			linesMu.Lock()
			lines = append(lines, time.Now().Format("15:04:05")+" "+s)
			if len(lines) > maxLines {
				lines = lines[len(lines)-maxLines:]
			}
			text := strings.Join(lines, "\n")
			n := len(lines)
			linesMu.Unlock()

			logView.SetText(text)
			logView.CursorRow = n
		})
	}
	setPct := func(fraction float64) {
		fyne.Do(func() {
			barInf.Stop()
			barInf.Hide()
			bar.Show()
			bar.SetValue(fraction)
		})
	}
	setStep := func(s string) {
		fyne.Do(func() { stepLabel.SetText(s) })
	}

	writer := &guiProgressWriter{onLine: appendLine, onPct: setPct}
	ctx, cancel := context.WithCancel(context.Background())
	running := true

	var cancelBtn, closeBtn *widget.Button
	cancelBtn = widget.NewButton("取消", func() {
		cancelBtn.Disable()
		appendLine("正在取消，请稍候...")
		cancel()
	})
	closeBtn = widget.NewButton("关闭", func() { w.Close() })
	closeBtn.Disable()

	top := container.NewVBox(
		widget.NewLabel("数据目录: "+cfgpkg.BaseDir),
		stepLabel,
		bar,
		barInf,
	)
	w.SetContent(container.NewBorder(top, container.NewHBox(cancelBtn, closeBtn), nil, nil, logView))
	w.SetCloseIntercept(func() {
		if running {
			cancel() // don't tear the window down mid-install; let the goroutine unwind
			return
		}
		w.Close()
	})
	w.Show()

	go func() {
		setStep("正在下载 / 校验 SteamCMD 与 ARK 服务端本体（约 25 GB，视网速可能较久）...")
		err := actions.InstallBaseEnvironment(ctx, writer)

		fyne.Do(func() {
			running = false
			barInf.Stop()
			barInf.Hide()
			bar.Show()
			bar.SetValue(1)
			cancelBtn.Disable()
			closeBtn.Enable()

			switch {
			case err != nil && ctx.Err() != nil:
				stepLabel.SetText("已取消")
			case err != nil:
				stepLabel.SetText("初始化失败：" + err.Error())
				stepLabel.Importance = widget.DangerImportance
			default:
				stepLabel.SetText("初始化完成，可以关闭此窗口")
				stepLabel.Importance = widget.SuccessImportance
				logger.Info("GUI environment setup completed")
			}
			stepLabel.Refresh()

			g.updateStatus()
			g.refreshEnvBanner()
		})
	}()
}
