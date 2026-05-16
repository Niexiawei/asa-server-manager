package gui

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"asa-server/serverinfo"
	"asa-server/webapi"
	"asa-server/winservice"
	_ "embed"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/kardianos/service"
	_ "golang.org/x/image/webp" // WebP decoder support
	"golang.org/x/sys/windows"
)

//go:embed ASA_Logo_transparent.webp
var trayIconData []byte

// ServiceStatus represents the status of the service
type ServiceStatus string

const (
	StatusRunning      ServiceStatus = "运行中"
	StatusStopped      ServiceStatus = "已停止"
	StatusInstalled    ServiceStatus = "已安装"
	StatusNotInstalled ServiceStatus = "未安装"
	StatusUnknown      ServiceStatus = "未知"
)

// InstanceInfo holds information about a game server instance
type InstanceInfo struct {
	Name    string
	Running bool
}

// GUIApp holds the GUI application state
type GUIApp struct {
	app         fyne.App
	window      fyne.Window
	statusLabel *widget.Label
	statusIcon  *widget.Icon
	trayStatus  ServiceStatus // Current service status for tray
	desktopApp  desktop.App   // Desktop app interface for tray updates
	// Resource monitoring
	cpuProgress      *widget.ProgressBar
	cpuLabel         *widget.Label
	memProgress      *widget.ProgressBar
	memLabel         *widget.Label
	memUsedLabel     *widget.Label
	resourceError    *widget.Label
	stopResourceChan chan struct{}
	// Instance list
	instances    []InstanceInfo
	instanceList *widget.List
	// Log viewer
	logWindow      fyne.Window
	logText        *widget.RichText
	logStatusLabel *widget.Label
	logScroll      *container.Scroll
	isLogStreaming bool
	autoScroll     bool
	stopLogFunc    func()
	// API server management
	apiServer      *webapi.APIServer
	apiServerMu    sync.Mutex
	apiRunning     bool
	apiStatusLabel *widget.Label
	startAPIBtn    *widget.Button
	stopAPIBtn     *widget.Button
}

// NewGUIApp creates a new GUI application
func NewGUIApp() *GUIApp {
	return &GUIApp{
		app:              app.NewWithID("com.asa.server.manager"),
		stopResourceChan: make(chan struct{}),
		isLogStreaming:   false,
		autoScroll:       true,
	}
}

// formatMemory formats bytes to human readable string
func formatMemory(bytes uint64) string {
	gb := float64(bytes) / (1024 * 1024 * 1024)
	if gb < 1 {
		mb := float64(bytes) / (1024 * 1024)
		return fmt.Sprintf("%.2f MB", mb)
	}
	return fmt.Sprintf("%.2f GB", gb)
}

// fetchResourceData fetches resource data directly using serverinfo package
func (g *GUIApp) fetchResourceData() {
	// Get CPU info
	cpuInfo, err := serverinfo.GetCPUInfo()
	if err != nil {
		fyne.Do(func() {
			if g.resourceError != nil {
				g.resourceError.SetText("获取CPU信息失败")
				g.resourceError.Show()
			}
		})
		return
	}

	// Get Memory info
	memInfo, err := serverinfo.GetMemoryInfo()
	if err != nil {
		fyne.Do(func() {
			if g.resourceError != nil {
				g.resourceError.SetText("获取内存信息失败")
				g.resourceError.Show()
			}
		})
		return
	}

	// Update UI
	fyne.Do(func() {
		g.updateResourceUI(cpuInfo, memInfo)
	})
}

// updateResourceUI updates the resource display UI
func (g *GUIApp) updateResourceUI(cpuInfo *serverinfo.CPUInfo, memInfo *serverinfo.MemoryInfo) {
	if g.resourceError != nil {
		g.resourceError.Hide()
	}

	// Update CPU
	if g.cpuProgress != nil {
		g.cpuProgress.SetValue(cpuInfo.UsedPercent / 100)
		g.cpuProgress.Refresh()
	}
	if g.cpuLabel != nil {
		g.cpuLabel.SetText(fmt.Sprintf("%.1f%% (%d核)", cpuInfo.UsedPercent, cpuInfo.CoreCount))
	}

	// Update Memory
	if g.memProgress != nil {
		g.memProgress.SetValue(memInfo.UsedPercent / 100)
		g.memProgress.Refresh()
	}
	if g.memLabel != nil {
		g.memLabel.SetText(fmt.Sprintf("%.2f%%", memInfo.UsedPercent))
	}
	if g.memUsedLabel != nil {
		g.memUsedLabel.SetText(fmt.Sprintf("%s / %s", formatMemory(memInfo.Used), formatMemory(memInfo.Total)))
	}
}

// startResourceMonitoring starts the resource monitoring goroutine
func (g *GUIApp) startResourceMonitoring() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		// Initial fetch
		g.fetchResourceData()

		for {
			select {
			case <-g.stopResourceChan:
				return
			case <-ticker.C:
				g.fetchResourceData()
			}
		}
	}()
}

// stopResourceMonitoring stops the resource monitoring
func (g *GUIApp) stopResourceMonitoring() {
	select {
	case <-g.stopResourceChan:
		// Already closed
	default:
		close(g.stopResourceChan)
	}
}

// fetchInstances fetches the list of game server instances
func (g *GUIApp) fetchInstances() {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		return
	}

	g.instances = make([]InstanceInfo, 0, len(instances))
	for _, name := range instances {
		running, err := asaserver.IsServerRunning(name)
		if err != nil {
			running = false
		}
		g.instances = append(g.instances, InstanceInfo{
			Name:    name,
			Running: running,
		})
	}
}

// startInstanceMonitoring starts monitoring instance status
func (g *GUIApp) startInstanceMonitoring() {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		// Initial fetch
		g.fetchInstances()
		fyne.Do(func() {
			if g.instanceList != nil {
				g.instanceList.Refresh()
			}
		})

		for {
			select {
			case <-g.stopResourceChan:
				return
			case <-ticker.C:
				g.fetchInstances()
				fyne.Do(func() {
					if g.instanceList != nil {
						g.instanceList.Refresh()
					}
				})
			}
		}
	}()
}

// getServiceStatus checks the current status of the service
func (g *GUIApp) getServiceStatus() (ServiceStatus, error) {
	s, err := service.New(nil, &service.Config{
		Name:        winservice.ServiceName,
		DisplayName: winservice.ServiceDisplayName,
		Description: winservice.ServiceDescription,
	})
	if err != nil {
		return StatusUnknown, err
	}

	status, err := s.Status()
	if err != nil {
		return StatusNotInstalled, nil
	}

	switch status {
	case service.StatusRunning:
		return StatusRunning, nil
	case service.StatusStopped:
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}

// updateStatus updates the status display
func (g *GUIApp) updateStatus() {
	status, err := g.getServiceStatus()
	if err != nil {
		fyne.Do(func() {
			g.statusLabel.SetText("状态: 错误 - " + err.Error())
		})
		return
	}

	// Update tray status
	g.trayStatus = status

	fyne.Do(func() {
		g.updateTrayMenu()

		switch status {
		case StatusRunning:
			g.statusLabel.SetText(fmt.Sprintf("状态: %s", status))
			g.statusIcon.SetResource(theme.NewSuccessThemedResource(theme.ConfirmIcon()))
		case StatusStopped, StatusInstalled:
			g.statusLabel.SetText(fmt.Sprintf("状态: %s", status))
			g.statusIcon.SetResource(theme.NewWarningThemedResource(theme.WarningIcon()))
		default:
			g.statusLabel.SetText(fmt.Sprintf("状态: %s", status))
			g.statusIcon.SetResource(theme.NewErrorThemedResource(theme.ErrorIcon()))
		}
	})
}

// updateTrayMenu updates the tray menu with current status
func (g *GUIApp) updateTrayMenu() {
	if g.desktopApp == nil {
		return
	}

	// Create status text with icon indicator
	statusText := fmt.Sprintf("● 服务状态: %s", g.trayStatus)

	menu := fyne.NewMenu("ASA Server Manager",
		fyne.NewMenuItem(statusText, nil), // Disabled status item
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("显示主窗口", func() {
			g.window.Show()
		}),
		fyne.NewMenuItem("打开 Web 界面", func() {
			g.openWebUI()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("启动服务", func() {
			g.startService()
		}),
		fyne.NewMenuItem("停止服务", func() {
			g.stopService()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("退出", func() {
			g.app.Quit()
		}),
	)
	g.desktopApp.SetSystemTrayMenu(menu)
}

// showError shows an error dialog
func (g *GUIApp) showError(err error) {
	dialog.ShowError(err, g.window)
}

// showSuccess shows a success dialog
func (g *GUIApp) showSuccess(message string) {
	dialog.ShowInformation("成功", message, g.window)
}

// showConfirm shows a confirmation dialog and calls callback if confirmed
func (g *GUIApp) showConfirm(title, message string, callback func()) {
	dialog.ShowConfirm(title, message, func(confirmed bool) {
		if confirmed {
			callback()
		}
	}, g.window)
}

// isAdmin checks if the current process is running with admin privileges
func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}

// runAsAdmin restarts the application with admin privileges
func runAsAdmin() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command("powershell", "-Command",
		fmt.Sprintf("Start-Process '%s' -Verb RunAs", exePath))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	return cmd.Start()
}

// installService installs the Windows service
func (g *GUIApp) installService() {
	// Check admin privileges
	if !isAdmin() {
		g.showConfirm("需要管理员权限", "安装服务需要管理员权限，是否以管理员身份重新启动？", func() {
			if err := runAsAdmin(); err != nil {
				g.showError(fmt.Errorf("启动管理员权限失败: %v", err))
			} else {
				g.app.Quit()
			}
		})
		return
	}

	// Confirm installation
	g.showConfirm("确认安装", "确定要安装 ASA Server 服务吗？", func() {
		if err := winservice.InstallService(); err != nil {
			g.showError(fmt.Errorf("安装服务失败: %v", err))
			return
		}

		g.showSuccess("服务安装成功!")
		g.updateStatus()
	})
}

// uninstallService removes the Windows service
func (g *GUIApp) uninstallService() {
	// Check admin privileges
	if !isAdmin() {
		g.showConfirm("需要管理员权限", "卸载服务需要管理员权限，是否以管理员身份重新启动？", func() {
			if err := runAsAdmin(); err != nil {
				g.showError(fmt.Errorf("启动管理员权限失败: %v", err))
			} else {
				g.app.Quit()
			}
		})
		return
	}

	// Confirm uninstallation
	g.showConfirm("确认卸载", "确定要卸载 ASA Server 服务吗？\n卸载前会先停止服务。", func() {
		if err := winservice.RemoveService(); err != nil {
			g.showError(fmt.Errorf("卸载服务失败: %v", err))
			return
		}

		g.showSuccess("服务卸载成功!")
		g.updateStatus()
	})
}

// startService starts the Windows service
func (g *GUIApp) startService() {
	if err := winservice.StartService(); err != nil {
		g.showError(fmt.Errorf("启动服务失败: %v", err))
		return
	}

	g.showSuccess("服务启动成功!")
	g.updateStatus()
}

// stopService stops the Windows service
func (g *GUIApp) stopService() {
	if err := winservice.StopService(); err != nil {
		g.showError(fmt.Errorf("停止服务失败: %v", err))
		return
	}

	g.showSuccess("服务停止成功!")
	g.updateStatus()
}

// startAPIServer starts the API server directly without Windows service
func (g *GUIApp) startAPIServer() {
	g.apiServerMu.Lock()
	if g.apiRunning {
		g.apiServerMu.Unlock()
		g.showError(fmt.Errorf("API 服务器已在运行中"))
		return
	}
	g.apiServerMu.Unlock()

	// Set log mode for HTTP API
	logger.SetLogMode(logger.HttpApiMode)

	// Create and start API server
	g.apiServerMu.Lock()
	g.apiServer = webapi.NewAPIServer()
	g.apiRunning = true
	g.updateAPIServerUI()
	g.apiServerMu.Unlock()

	go func() {
		if err := g.apiServer.Start(); err != nil {
			logger.GetLogger().Errorf("API server stopped with error: %v", err)
			g.apiServerMu.Lock()
			g.apiRunning = false
			g.updateAPIServerUI()
			g.apiServerMu.Unlock()
		}
	}()

	g.showSuccess("API 服务器启动成功!\n访问 http://localhost:19193")
}

// stopAPIServer stops the API server
func (g *GUIApp) stopAPIServer() {
	g.apiServerMu.Lock()
	if !g.apiRunning || g.apiServer == nil {
		g.apiServerMu.Unlock()
		g.showError(fmt.Errorf("API 服务器未运行"))
		return
	}
	server := g.apiServer
	g.apiServerMu.Unlock()

	// Stop server (this blocks until shutdown completes)
	if err := server.Stop(); err != nil {
		g.showError(fmt.Errorf("停止 API 服务器失败: %v", err))
		return
	}

	g.apiServerMu.Lock()
	g.apiRunning = false
	g.apiServer = nil
	g.updateAPIServerUI()
	g.apiServerMu.Unlock()

	g.showSuccess("API 服务器已停止")
}

// updateAPIServerUI updates the API server status label and button states
func (g *GUIApp) updateAPIServerUI() {
	if g.apiRunning {
		g.apiStatusLabel.SetText("状态: ● 运行中")
		g.apiStatusLabel.Importance = widget.HighImportance
		g.startAPIBtn.Disable()
		g.stopAPIBtn.Enable()
	} else {
		g.apiStatusLabel.SetText("状态: ● 已停止")
		g.apiStatusLabel.Importance = widget.MediumImportance
		g.startAPIBtn.Enable()
		g.stopAPIBtn.Disable()
	}
}

// openWebUI opens the web UI in browser
func (g *GUIApp) openWebUI() {
	url := "http://localhost:19193"
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	err := cmd.Start()
	if err != nil {
		g.showError(fmt.Errorf("打开浏览器失败: %v", err))
	}
}

// confirmAndQuit shows a confirmation dialog before quitting
func (g *GUIApp) confirmAndQuit() {
	g.showConfirm("确认退出", "确定要退出 ASA Server 管理器吗？", func() {
		// Stop API server if running
		g.apiServerMu.Lock()
		if g.apiRunning && g.apiServer != nil {
			server := g.apiServer
			g.apiServerMu.Unlock()
			server.Stop()
		} else {
			g.apiServerMu.Unlock()
		}
		g.app.Quit()
	})
}

// showMainWindow shows the main window with layout fix for Windows
func (g *GUIApp) showMainWindow() {
	// Fix for Windows: Hide then show to force window recreation
	g.window.Hide()
	g.window.Show()
	g.window.RequestFocus()
}

// createTray creates the system tray icon and menu
func (g *GUIApp) createTray() {
	if desk, ok := g.app.(desktop.App); ok {
		g.desktopApp = desk

		// Initial tray menu (will be updated when status is checked)
		menu := fyne.NewMenu("ASA Server Manager",
			fyne.NewMenuItem("● 服务状态: 检查中...", nil),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("显示主窗口", func() {
				g.showMainWindow()
			}),
			fyne.NewMenuItem("打开 Web 界面", func() {
				g.openWebUI()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("启动 API 服务器", func() {
				g.startAPIServer()
			}),
			fyne.NewMenuItem("停止 API 服务器", func() {
				g.stopAPIServer()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("启动服务", func() {
				g.startService()
			}),
			fyne.NewMenuItem("停止服务", func() {
				g.stopService()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("退出", func() {
				g.confirmAndQuit()
			}),
		)
		desk.SetSystemTrayMenu(menu)

		// Load tray icon from embedded webp file
		if len(trayIconData) > 0 {
			icon := fyne.NewStaticResource("ASA_Logo_transparent.webp", trayIconData)
			desk.SetSystemTrayIcon(icon)
		} else {
			// Fallback to default icon
			desk.SetSystemTrayIcon(theme.ComputerIcon())
		}
	}
}

// createMainWindow creates the main window
func (g *GUIApp) createMainWindow() {
	g.window = g.app.NewWindow("ASA Server Manager")
	g.window.SetMaster()
	g.window.Resize(fyne.NewSize(1200, 600))

	// Set window icon from embedded webp file
	if len(trayIconData) > 0 {
		icon := fyne.NewStaticResource("ASA_Logo_transparent.webp", trayIconData)
		g.window.SetIcon(icon)
	}

	// Set window close behavior - minimize to tray instead of closing
	g.window.SetCloseIntercept(func() {
		g.window.Hide()
	})

	// ==================== Left Panel - Controls ====================

	// Service Status Section
	statusSection := widget.NewLabel("服务状态")
	statusSection.TextStyle = fyne.TextStyle{Bold: true}

	g.statusLabel = widget.NewLabel("状态: 检查中...")
	g.statusLabel.Alignment = fyne.TextAlignCenter
	g.statusIcon = widget.NewIcon(theme.QuestionIcon())

	// Status row - left aligned with vertical centering
	statusRow := container.NewHBox(g.statusIcon, g.statusLabel)
	statusBox := container.NewPadded(statusRow)

	// Resource Monitor Section
	resourceSection := widget.NewLabel("资源监控")
	resourceSection.TextStyle = fyne.TextStyle{Bold: true}

	// CPU Progress - use Border layout for alignment
	cpuLabelTitle := widget.NewLabel("CPU 使用率:")
	cpuLabelTitleContainer := container.NewGridWithColumns(1, cpuLabelTitle)
	g.cpuLabel = widget.NewLabel("--")
	g.cpuLabel.Alignment = fyne.TextAlignTrailing
	g.cpuProgress = widget.NewProgressBar()
	g.cpuProgress.Min = 0
	g.cpuProgress.Max = 1

	cpuBox := container.NewVBox(
		container.NewBorder(nil, nil, cpuLabelTitleContainer, nil, g.cpuLabel),
		g.cpuProgress,
	)

	// Memory Progress - use Border layout for alignment
	memLabelTitle := widget.NewLabel("内存使用率:")
	memLabelTitleContainer := container.NewGridWithColumns(1, memLabelTitle)
	g.memLabel = widget.NewLabel("--")
	g.memLabel.Alignment = fyne.TextAlignTrailing
	g.memProgress = widget.NewProgressBar()
	g.memProgress.Min = 0
	g.memProgress.Max = 1
	g.memUsedLabel = widget.NewLabel("-- / --")

	memBox := container.NewVBox(
		container.NewBorder(nil, nil, memLabelTitleContainer, nil, g.memLabel),
		g.memProgress,
		g.memUsedLabel,
	)

	// Resource error label
	g.resourceError = widget.NewLabel("")
	g.resourceError.Hide()

	resourceBox := container.NewVBox(
		cpuBox,
		widget.NewSeparator(),
		memBox,
		g.resourceError,
	)

	// Buttons Section
	buttonsSection := widget.NewLabel("服务管理")
	buttonsSection.TextStyle = fyne.TextStyle{Bold: true}

	installBtn := widget.NewButton("安装服务", func() {
		g.installService()
	})
	installBtn.Importance = widget.HighImportance

	uninstallBtn := widget.NewButton("卸载服务", func() {
		g.uninstallService()
	})

	startBtn := widget.NewButton("启动服务", func() {
		g.startService()
	})

	stopBtn := widget.NewButton("停止服务", func() {
		g.stopService()
	})

	refreshBtn := widget.NewButtonWithIcon("刷新状态", theme.ViewRefreshIcon(), func() {
		g.updateStatus()
	})

	openWebBtn := widget.NewButtonWithIcon("打开 Web 界面", theme.FolderOpenIcon(), func() {
		g.openWebUI()
	})

	exitBtn := widget.NewButtonWithIcon("退出", theme.LogoutIcon(), func() {
		g.confirmAndQuit()
	})
	exitBtn.Importance = widget.DangerImportance

	// Service buttons - buttons fill the grid columns
	serviceButtons := container.NewGridWithColumns(2,
		makeButtonBox(installBtn),
		makeButtonBox(uninstallBtn),
		makeButtonBox(startBtn),
		makeButtonBox(stopBtn),
	)

	// Other buttons - buttons fill the grid columns
	otherButtons := container.NewGridWithColumns(2,
		makeButtonBox(refreshBtn),
		makeButtonBox(openWebBtn),
	)

	// API Server Section
	apiSection := widget.NewLabel("API 服务器")
	apiSection.TextStyle = fyne.TextStyle{Bold: true}

	g.apiStatusLabel = widget.NewLabel("状态: ● 已停止")
	g.apiStatusLabel.Importance = widget.MediumImportance

	g.startAPIBtn = widget.NewButton("启动 API", func() {
		g.startAPIServer()
	})
	g.startAPIBtn.Importance = widget.HighImportance

	g.stopAPIBtn = widget.NewButton("停止 API", func() {
		g.stopAPIServer()
	})
	g.stopAPIBtn.Disable()

	apiButtons := container.NewGridWithColumns(2,
		makeButtonBox(g.startAPIBtn),
		makeButtonBox(g.stopAPIBtn),
	)

	// Exit button row
	exitButtons := container.NewGridWithColumns(1,
		makeButtonBox(exitBtn),
	)

	// Left panel content
	leftPanel := container.NewVBox(
		statusSection,
		statusBox,
		widget.NewSeparator(),
		resourceSection,
		container.NewPadded(resourceBox),
		widget.NewSeparator(),
		buttonsSection,
		serviceButtons,
		otherButtons,
		widget.NewSeparator(),
		apiSection,
		g.apiStatusLabel,
		apiButtons,
		widget.NewSeparator(),
		exitButtons,
	)

	// ==================== Right Panel - Log Viewer ====================

	// Log title
	logTitle := widget.NewLabel("实时系统日志")
	logTitle.TextStyle = fyne.TextStyle{Bold: true}

	// Log text area - RichText with white text on black background
	g.logText = widget.NewRichText()
	g.logText.Wrapping = fyne.TextWrapBreak

	// Create black background
	logBg := canvas.NewRectangle(color.NRGBA{R: 15, G: 15, B: 15, A: 255})
	logContainer := container.NewStack(logBg, container.NewPadded(g.logText))

	// Scrollable container for log text
	g.logScroll = container.NewScroll(logContainer)

	// Status label
	g.logStatusLabel = widget.NewLabel("状态: 已停止")

	// Auto scroll checkbox
	autoScrollCheck := widget.NewCheck("自动滚动", func(checked bool) {
		g.autoScroll = checked
	})
	autoScrollCheck.SetChecked(g.autoScroll)

	// Control buttons
	startLogBtn := widget.NewButtonWithIcon("开始监听", theme.MediaPlayIcon(), func() {
		g.startLogStreaming()
	})

	stopLogBtn := widget.NewButtonWithIcon("停止监听", theme.MediaStopIcon(), func() {
		g.stopLogStreaming()
	})

	clearBtn := widget.NewButtonWithIcon("清空日志", theme.DeleteIcon(), func() {
		g.clearLogs()
	})

	refreshLogBtn := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), func() {
		g.clearLogs()
		if g.isLogStreaming {
			g.stopLogStreaming()
			g.startLogStreaming()
		}
	})

	// Store button references for state management
	logButtons = &LogViewerButtons{
		startBtn:   startLogBtn,
		stopBtn:    stopLogBtn,
		clearBtn:   clearBtn,
		refreshBtn: refreshLogBtn,
	}

	// Initial button state
	g.updateLogButtonsState()

	// Button container
	logButtonContainer := container.NewHBox(
		startLogBtn,
		stopLogBtn,
		clearBtn,
		refreshLogBtn,
		widget.NewSeparator(),
		g.logStatusLabel,
		widget.NewSeparator(),
		autoScrollCheck,
	)

	// Right panel content
	rightPanel := container.NewBorder(
		container.NewVBox(
			logTitle,
			widget.NewSeparator(),
		),
		logButtonContainer,
		nil,
		nil,
		g.logScroll,
	)

	// ==================== Main Layout ====================

	content := container.NewHSplit(
		container.NewPadded(leftPanel),
		rightPanel,
	)
	content.SetOffset(0.4)

	// Set content
	g.window.SetContent(content)
}

// Run starts the GUI application
func (g *GUIApp) Run() {
	// Set theme
	g.app.Settings().SetTheme(&myTheme{})

	// Set application icon (for taskbar)
	if len(trayIconData) > 0 {
		icon := fyne.NewStaticResource("ASA_Logo_transparent.webp", trayIconData)
		g.app.SetIcon(icon)
	}

	// Create tray icon
	g.createTray()

	// Create main window
	g.createMainWindow()

	// Initial status update
	go func() {
		time.Sleep(100 * time.Millisecond)
		g.updateStatus()
	}()

	// Start resource monitoring
	g.startResourceMonitoring()

	// Auto-start log streaming after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		g.startLogStreaming()
	}()

	// Show window
	g.window.ShowAndRun()

	// Stop resource monitoring when app exits
	g.stopResourceMonitoring()
}

// myTheme is a custom theme
type myTheme struct{}

func (m *myTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, variant)
}

func (m *myTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (m *myTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (m *myTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

// logTheme is a dark theme for log viewer with white text and visible scrollbar
type logTheme struct{}

func (t *logTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameForeground:
		return color.White // White text
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 100, G: 100, B: 100, A: 200} // Visible scrollbar
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 30, G: 30, B: 30, A: 255} // Dark input background
	case theme.ColorNameBackground:
		return color.NRGBA{R: 20, G: 20, B: 20, A: 255} // Dark background
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 128, G: 128, B: 128, A: 255} // Gray placeholder
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 200, G: 200, B: 200, A: 255} // Disabled text (still visible)
	default:
		return theme.DefaultTheme().Color(name, variant)
	}
}

func (t *logTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *logTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *logTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

// makeButtonBox creates a button container that fills available width
func makeButtonBox(btn *widget.Button) *fyne.Container {
	// Use Border layout to make button fill horizontal space
	return container.NewBorder(nil, nil, nil, nil, btn)
}

// ==================== System Log Viewer ====================

// LogViewerButtons holds references to log viewer buttons for state management
type LogViewerButtons struct {
	startBtn   *widget.Button
	stopBtn    *widget.Button
	clearBtn   *widget.Button
	refreshBtn *widget.Button
}

var logButtons *LogViewerButtons

// getLogFilePath returns the path to the system log file
func (g *GUIApp) getLogFilePath() string {
	// Try to get from logger package first
	logPath := logger.GetLogFilePath()
	if logPath != "" {
		return logPath
	}
	// Fallback to default path
	return filepath.Join("logs", "asaServer.log")
}

// updateLogButtonsState updates button states based on streaming status
func (g *GUIApp) updateLogButtonsState() {
	if logButtons == nil {
		return
	}
	fyne.Do(func() {
		logButtons.startBtn.Disable()
		logButtons.stopBtn.Disable()
		logButtons.clearBtn.Disable()
		logButtons.refreshBtn.Disable()

		if g.isLogStreaming {
			logButtons.stopBtn.Enable()
			logButtons.refreshBtn.Enable()
		} else {
			logButtons.startBtn.Enable()
			logButtons.clearBtn.Enable()
			logButtons.refreshBtn.Enable()
		}
	})
}

// startLogStreaming starts the log streaming using asaserver.TailLogFileWithLines
func (g *GUIApp) startLogStreaming() {
	if g.isLogStreaming {
		return
	}

	g.isLogStreaming = true
	g.updateLogButtonsState()

	// Update UI status
	fyne.Do(func() {
		if g.logStatusLabel != nil {
			g.logStatusLabel.SetText("状态: 监听中...")
		}
	})

	// Use asaserver.TailLogFileWithLines for efficient log tailing
	logPath := g.getLogFilePath()
	g.stopLogFunc = asaserver.TailLogFileWithLines(logPath, 100, func(line string) {
		fyne.Do(func() {
			if g.logText != nil {
				// Create white text segment
				segment := &widget.TextSegment{
					Style: widget.RichTextStyle{
						ColorName: theme.ColorNameForeground,
						TextStyle: fyne.TextStyle{Monospace: true},
					},
					Text: line + "\n",
				}

				// Append new segment
				g.logText.Segments = append(g.logText.Segments, segment)
				g.logText.Refresh()

				// Auto scroll to bottom only if auto scroll is enabled
				if g.autoScroll && g.logScroll != nil {
					g.logScroll.ScrollToBottom()
				}
			}
		})
	})
}

// stopLogStreaming stops the log streaming
func (g *GUIApp) stopLogStreaming() {
	if !g.isLogStreaming {
		return
	}

	g.isLogStreaming = false
	if g.stopLogFunc != nil {
		g.stopLogFunc()
		g.stopLogFunc = nil
	}
	g.updateLogButtonsState()

	// Update UI status
	fyne.Do(func() {
		if g.logStatusLabel != nil {
			g.logStatusLabel.SetText("状态: 已停止")
		}
	})
}

// clearLogs clears the log text
func (g *GUIApp) clearLogs() {
	if g.logText != nil {
		g.logText.Segments = nil
		g.logText.Refresh()
	}
}

// openLogViewer opens the log viewer window
func (g *GUIApp) openLogViewer() {
	// If window already exists, just show it and start streaming
	if g.logWindow != nil {
		g.logWindow.Show()
		g.logWindow.RequestFocus()
		// Auto start streaming if not already streaming
		if !g.isLogStreaming {
			g.startLogStreaming()
		}
		return
	}

	// Create log viewer window
	g.logWindow = g.app.NewWindow("系统日志查看器")
	g.logWindow.Resize(fyne.NewSize(900, 600))

	// Set window close behavior
	g.logWindow.SetCloseIntercept(func() {
		g.stopLogStreaming()
		g.logWindow.Hide()
	})

	// Log text area - RichText with white text on black background
	g.logText = widget.NewRichText()
	g.logText.Wrapping = fyne.TextWrapBreak

	// Create black background
	logBg := canvas.NewRectangle(color.NRGBA{R: 15, G: 15, B: 15, A: 255})
	logContainer := container.NewStack(logBg, container.NewPadded(g.logText))

	// Scrollable container for log text
	g.logScroll = container.NewScroll(logContainer)
	g.logScroll.SetMinSize(fyne.NewSize(880, 480))

	// Status label
	g.logStatusLabel = widget.NewLabel("状态: 已停止")

	// Auto scroll checkbox
	autoScrollCheck := widget.NewCheck("自动滚动", func(checked bool) {
		g.autoScroll = checked
	})
	autoScrollCheck.SetChecked(g.autoScroll)

	// Control buttons
	startBtn := widget.NewButtonWithIcon("开始监听", theme.MediaPlayIcon(), func() {
		g.startLogStreaming()
	})

	stopBtn := widget.NewButtonWithIcon("停止监听", theme.MediaStopIcon(), func() {
		g.stopLogStreaming()
	})

	clearBtn := widget.NewButtonWithIcon("清空日志", theme.DeleteIcon(), func() {
		g.clearLogs()
	})

	refreshBtn := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), func() {
		g.clearLogs()
		if g.isLogStreaming {
			g.stopLogStreaming()
			g.startLogStreaming()
		}
	})

	closeBtn := widget.NewButtonWithIcon("关闭窗口", theme.CancelIcon(), func() {
		g.stopLogStreaming()
		g.logWindow.Hide()
	})

	// Store button references for state management
	logButtons = &LogViewerButtons{
		startBtn:   startBtn,
		stopBtn:    stopBtn,
		clearBtn:   clearBtn,
		refreshBtn: refreshBtn,
	}

	// Initial button state
	g.updateLogButtonsState()

	// Button container
	buttonContainer := container.NewHBox(
		startBtn,
		stopBtn,
		clearBtn,
		refreshBtn,
		widget.NewSeparator(),
		g.logStatusLabel,
		widget.NewSeparator(),
		autoScrollCheck,
		container.NewHBox(layout.NewSpacer()),
		closeBtn,
	)

	// Main content with black background
	content := container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("实时系统日志", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewSeparator(),
		),
		buttonContainer,
		nil,
		nil,
		g.logScroll,
	)

	g.logWindow.SetContent(content)

	// Start streaming automatically
	g.startLogStreaming()

	g.logWindow.Show()
}
