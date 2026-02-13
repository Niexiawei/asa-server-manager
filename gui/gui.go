package gui

import (
	"asa-server/serverinfo"
	"asa-server/winservice"
	_ "embed"
	"fmt"
	"image/color"
	"os"
	"os/exec"
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
}

// NewGUIApp creates a new GUI application
func NewGUIApp() *GUIApp {
	return &GUIApp{
		app:              app.NewWithID("com.asa.server.manager"),
		stopResourceChan: make(chan struct{}),
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
		prg := &program{}
		s, err := service.New(prg, &service.Config{
			Name:        winservice.ServiceName,
			DisplayName: winservice.ServiceDisplayName,
			Description: winservice.ServiceDescription,
			Arguments:   []string{"service-run"},
		})
		if err != nil {
			g.showError(fmt.Errorf("创建服务失败: %v", err))
			return
		}

		err = s.Install()
		if err != nil {
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
		prg := &program{}
		s, err := service.New(prg, &service.Config{
			Name:        winservice.ServiceName,
			DisplayName: winservice.ServiceDisplayName,
			Description: winservice.ServiceDescription,
		})
		if err != nil {
			g.showError(fmt.Errorf("创建服务失败: %v", err))
			return
		}

		// Try to stop first
		_ = s.Stop()
		time.Sleep(500 * time.Millisecond)

		err = s.Uninstall()
		if err != nil {
			g.showError(fmt.Errorf("卸载服务失败: %v", err))
			return
		}

		g.showSuccess("服务卸载成功!")
		g.updateStatus()
	})
}

// startService starts the Windows service
func (g *GUIApp) startService() {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        winservice.ServiceName,
		DisplayName: winservice.ServiceDisplayName,
		Description: winservice.ServiceDescription,
	})
	if err != nil {
		g.showError(fmt.Errorf("创建服务失败: %v", err))
		return
	}

	err = s.Start()
	if err != nil {
		g.showError(fmt.Errorf("启动服务失败: %v", err))
		return
	}

	g.showSuccess("服务启动成功!")
	g.updateStatus()
}

// stopService stops the Windows service
func (g *GUIApp) stopService() {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        winservice.ServiceName,
		DisplayName: winservice.ServiceDisplayName,
		Description: winservice.ServiceDescription,
	})
	if err != nil {
		g.showError(fmt.Errorf("创建服务失败: %v", err))
		return
	}

	err = s.Stop()
	if err != nil {
		g.showError(fmt.Errorf("停止服务失败: %v", err))
		return
	}

	g.showSuccess("服务停止成功!")
	g.updateStatus()
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
		g.app.Quit()
	})
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
	g.window.Resize(fyne.NewSize(380, 520))
	g.window.SetFixedSize(true)

	// Set window icon from embedded webp file
	if len(trayIconData) > 0 {
		icon := fyne.NewStaticResource("ASA_Logo_transparent.webp", trayIconData)
		g.window.SetIcon(icon)
	}

	// Set window close behavior - minimize to tray instead of closing
	g.window.SetCloseIntercept(func() {
		g.window.Hide()
	})

	// Service Status Section
	statusSection := widget.NewLabel("服务状态")
	statusSection.TextStyle = fyne.TextStyle{Bold: true}

	g.statusLabel = widget.NewLabel("状态: 检查中...")
	g.statusIcon = widget.NewIcon(theme.QuestionIcon())

	// Status row with vertical centering
	iconBox := container.NewCenter(g.statusIcon)
	labelBox := container.NewCenter(g.statusLabel)
	statusRow := container.NewHBox(iconBox, labelBox)
	statusBox := container.NewPadded(statusRow)

	// Resource Monitor Section
	resourceSection := widget.NewLabel("资源监控")
	resourceSection.TextStyle = fyne.TextStyle{Bold: true}

	// CPU Progress
	cpuLabelTitle := widget.NewLabel("CPU 使用率:")
	g.cpuLabel = widget.NewLabel("--")
	g.cpuProgress = widget.NewProgressBar()
	g.cpuProgress.Min = 0
	g.cpuProgress.Max = 1

	cpuBox := container.NewVBox(
		container.NewHBox(cpuLabelTitle, g.cpuLabel),
		g.cpuProgress,
	)

	// Memory Progress
	memLabelTitle := widget.NewLabel("内存使用率:")
	g.memLabel = widget.NewLabel("--")
	g.memProgress = widget.NewProgressBar()
	g.memProgress.Min = 0
	g.memProgress.Max = 1
	g.memUsedLabel = widget.NewLabel("-- / --")

	memBox := container.NewVBox(
		container.NewHBox(memLabelTitle, g.memLabel),
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

	// Exit button row
	exitButtons := container.NewGridWithColumns(1,
		makeButtonBox(exitBtn),
	)

	// Main content - removed title label to save space
	content := container.NewVBox(
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
		exitButtons,
	)

	// Set content with padding
	g.window.SetContent(container.NewPadded(content))
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

// program implements the service.Service interface for service operations
type program struct{}

func (p *program) Start(s service.Service) error {
	return nil
}

func (p *program) Stop(s service.Service) error {
	return nil
}

// makeButtonBox creates a button container that fills available width
func makeButtonBox(btn *widget.Button) *fyne.Container {
	// Use Border layout to make button fill horizontal space
	return container.NewBorder(nil, nil, nil, nil, btn)
}
