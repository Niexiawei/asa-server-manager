package gui

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
	procpkg "asa-server/internal/process"
	"asa-server/internal/webapi"
	"asa-server/internal/winservice"
	"asa-server/pkg/serverinfo"
	"context"
	_ "embed"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
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
	cpuProgress    *widget.ProgressBar
	cpuLabel       *widget.Label
	memProgress    *widget.ProgressBar
	memLabel       *widget.Label
	memUsedLabel   *widget.Label
	resourceError  *widget.Label
	resourceCtx    context.Context    // cancelled to stop all background monitors
	resourceCancel context.CancelFunc // cancel function
	// Instance list
	instances    []InstanceInfo
	instanceList *widget.List
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
	ctx, cancel := context.WithCancel(context.Background())
	return &GUIApp{
		app:            app.NewWithID("com.asa.server.manager"),
		resourceCtx:    ctx,
		resourceCancel: cancel,
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
			case <-g.resourceCtx.Done():
				return
			case <-ticker.C:
				g.fetchResourceData()
			}
		}
	}()
}

// stopResourceMonitoring stops the resource monitoring
func (g *GUIApp) stopResourceMonitoring() {
	g.resourceCancel()
}

// fetchInstances fetches the list of game server instances
func (g *GUIApp) fetchInstances() {
	instances, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		return
	}

	g.instances = make([]InstanceInfo, 0, len(instances))
	for _, name := range instances {
		running, err := procpkg.IsServerRunning(name)
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
			case <-g.resourceCtx.Done():
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

	g.showSuccess(fmt.Sprintf("API 服务器启动成功!\n访问 %s", webUIURL()))
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

// webUIURL 按当前 TLS 开关拼出 WebUI 地址：启用 TLS 时必须是 https，
// 否则浏览器会用 http 去敲一个 TLS 端口，直接连不上
func webUIURL() string {
	return fmt.Sprintf("%s://localhost:%d", webapi.Scheme(), webapi.ApiServerPort)
}

// openWebUI opens the web UI in browser
func (g *GUIApp) openWebUI() {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", webUIURL())
	err := cmd.Start()
	if err != nil {
		g.showError(fmt.Errorf("打开浏览器失败: %v", err))
	}
}

// confirmAndQuit shows a confirmation dialog before quitting
func (g *GUIApp) confirmAndQuit() {
	g.showConfirm("确认退出", "确定要退出 ASA Server 管理器吗？", func() {
		g.apiServerMu.Lock()
		if g.apiRunning && g.apiServer != nil {
			server := g.apiServer
			g.apiServerMu.Unlock()
			// Stop API server in goroutine with timeout to avoid blocking UI
			go func() {
				done := make(chan struct{})
				go func() {
					server.Stop()
					close(done)
				}()
				select {
				case <-done:
					// Clean shutdown
				case <-time.After(10 * time.Second):
					logger.GetLogger().Warn("API server stop timed out after 10s")
				}
				g.app.Quit()
			}()
		} else {
			g.apiServerMu.Unlock()
			g.app.Quit()
		}
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
	g.window.Resize(fyne.NewSize(500, 600))

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

	// Set content
	g.window.SetContent(container.NewPadded(leftPanel))
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

	// Initial status update - delay to allow window rendering to complete;
	// Fyne does not provide a reliable "window shown" callback on Windows,
	// so we use a short delay as a pragmatic workaround.
	go func() {
		time.Sleep(100 * time.Millisecond)
		g.updateStatus()
	}()

	// Start resource monitoring
	g.startResourceMonitoring()

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

// makeButtonBox creates a button container that fills available width
func makeButtonBox(btn *widget.Button) *fyne.Container {
	// Use Border layout to make button fill horizontal space
	return container.NewBorder(nil, nil, nil, nil, btn)
}
