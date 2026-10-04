//go:build windows

package gui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"asa-server/internal/appconfig"
	"asa-server/internal/bootstrap"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/webapi"
	"asa-server/pkg/folderpicker"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/userenv"
)

// 首次启动向导：配置位置 → 数据目录 → 生成并检查配置 → 初始化环境。
//
// 三级查找都没有 config.yaml 时（appconfig.Load 只读，缺失时文件确实不存在）由
// runFirstLaunchWizardIfNeeded 打开。它等价于 CLI 的
// `config init --dir --basedir [--set-env]` + 编辑 + `config validate` + `setup`
// （§10.7 不变量 2：向导做的事都有 CLI 等价物）。
// 见 docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.5。

type cfgLocation int

const (
	locExe cfgLocation = iota
	locSystem
	locCustom
)

type configWizard struct {
	g *GUIApp
	w fyne.Window

	exeDir, sysDir string
	exeWritable    bool

	// ASA_CFG 的现状。lockedDir 非空 = 由系统级环境变量或启动时的进程环境给定，
	// 本向导不去覆盖（用户级写一个去盖住管理员的设定属于越权），只读显示。
	userCfg          string
	userCfgSet       bool
	lockedDir        string
	lockedSource     string
	serviceInstalled bool

	loc    cfgLocation
	cfgDir string // 第 1 页选定的配置目录
	path   string // 生成（或接管）的 config.yaml
	// generated：配置已经落盘（生成或接管）。此后关窗 = 「稍后再初始化」，
	// 在此之前关窗 = 走默认值兜底。
	generated bool
	adopted   bool
	envNote   string // 第 3 页展示的 ASA_CFG 处理结果
	busy      bool
}

// runFirstLaunchWizardIfNeeded 在全新安装（三级查找都没有 config.yaml）时打开首次
// 启动向导；任一级已有配置（哪怕没有 basedir 字段）都维持现状，见
// docs/LINUX_COMPATIBILITY_PLAN.md §10.4。必须在 g.window 创建之后调用。
func (g *GUIApp) runFirstLaunchWizardIfNeeded() {
	if !appconfig.ConfigMissing() {
		return
	}
	g.showConfigWizard()
}

func (g *GUIApp) showConfigWizard() {
	cw := newConfigWizard(g)
	cw.w = g.app.NewWindow("首次设置")
	cw.w.Resize(fyne.NewSize(680, 460))
	cw.w.SetCloseIntercept(cw.cancel)
	cw.showLocationPage()
	cw.w.Show()
	// 主窗口先创建、后显示完成，不抢一下焦点的话向导会被压在主窗口下面——首次
	// 启动的用户根本看不到它（实测）。
	cw.w.RequestFocus()
}

func newConfigWizard(g *GUIApp) *configWizard {
	cw := &configWizard{g: g}
	if dirs, err := appconfig.ConfigSearchDirs(); err == nil {
		cw.exeDir, cw.sysDir = dirs.ExeDir, dirs.SystemDir
	}
	cw.exeWritable = dirWritable(cw.exeDir)

	// 用户级 / 系统级直接读注册表：os.Getenv 分不清来源。
	cw.userCfg, cw.userCfgSet, _ = userenv.Get(userenv.User, "ASA_CFG")
	machine, machineSet, _ := userenv.Get(userenv.Machine, "ASA_CFG")
	proc := os.Getenv("ASA_CFG")
	switch {
	case cw.userCfgSet:
		cw.loc = locCustom // 多半是上次向导写的
	case machineSet:
		cw.lockedDir, cw.lockedSource = machine, "系统级环境变量（管理员设置）"
	case proc != "":
		cw.lockedDir, cw.lockedSource = proc, "启动本程序时的环境变量"
	case !cw.exeWritable && cw.sysDir != "":
		cw.loc = locSystem
	default:
		cw.loc = locExe
	}
	if status, err := g.getServiceStatus(); err == nil {
		cw.serviceInstalled = status != StatusNotInstalled && status != StatusUnknown
	}
	return cw
}

// ---------------------------------------------------------------- 第 1 页

func (cw *configWizard) showLocationPage() {
	title := pageTitle("1/3  配置文件位置")

	if cw.lockedDir != "" {
		info := wrapLabel(fmt.Sprintf("环境变量 ASA_CFG 已由%s设为：\n%s\n\n程序只会在这里查找配置文件，本页不能更改。",
			cw.lockedSource, cw.lockedDir))
		next := widget.NewButton("下一步", func() { cw.confirmLocation(cw.lockedDir, false) })
		next.Importance = widget.HighImportance
		cw.setPage(container.NewVBox(title, info), widget.NewButton("取消", cw.cancel), next)
		return
	}

	customEntry := widget.NewEntry()
	customEntry.SetPlaceHolder(`例如 E:\ASA-Config`)
	if cw.userCfgSet {
		customEntry.SetText(cw.userCfg)
	}
	browse := widget.NewButton("浏览…", nil)
	browse.OnTapped = func() { cw.pickFolder("选择配置文件目录", customEntry, browse) }
	customRow := container.NewBorder(nil, nil, nil, browse, customEntry)

	serviceWarn := wrapLabel("检测到已安装服务：服务使用的是安装时记录的配置位置。改动后需要以管理员身份重新安装服务，服务才会读到新位置。")
	serviceWarn.Importance = widget.WarningImportance
	serviceWarn.Hide()

	optExe := "程序目录（推荐，绿色部署）  " + cw.exeDir
	if !cw.exeWritable {
		optExe += "  —— 当前用户无写权限"
	}
	optSys := "系统目录  " + cw.sysDir
	optCustom := "自定义目录（会为当前用户设置环境变量 ASA_CFG 指向它）"
	options := []string{optExe}
	if cw.sysDir != "" {
		options = append(options, optSys)
	}
	options = append(options, optCustom)

	byLabel := map[string]cfgLocation{optExe: locExe, optSys: locSystem, optCustom: locCustom}
	refresh := func() {
		if cw.loc == locCustom {
			customEntry.Enable()
			browse.Enable()
		} else {
			customEntry.Disable()
			browse.Disable()
		}
		if cw.serviceInstalled && cw.asaCfgWouldChange(strings.TrimSpace(customEntry.Text)) {
			serviceWarn.Show()
		} else {
			serviceWarn.Hide()
		}
	}
	radio := widget.NewRadioGroup(options, func(sel string) {
		if loc, ok := byLabel[sel]; ok {
			cw.loc = loc
		}
		refresh()
	})
	radio.Required = true
	for label, loc := range byLabel {
		if loc == cw.loc {
			radio.SetSelected(label)
		}
	}
	customEntry.OnChanged = func(string) { refresh() }
	refresh()

	// 别用「→」：Fyne 自动换行恰好断在它附近时会渲染成乱码（实测）。
	hint := wrapLabel("程序启动时依次在环境变量 ASA_CFG 指定的目录、程序目录、系统目录中查找配置。" +
		"放在程序目录不可写的位置（如 Program Files）时请选系统目录；放在别处请选自定义目录。")
	hint.Importance = widget.LowImportance

	next := widget.NewButton("下一步", func() {
		switch cw.loc {
		case locExe:
			if !cw.exeWritable {
				cw.showError(fmt.Errorf("当前用户对程序目录 %s 没有写权限，请选系统目录或自定义目录", cw.exeDir))
				return
			}
			cw.confirmLocation(cw.exeDir, false)
		case locSystem:
			cw.confirmLocation(cw.sysDir, false)
		default:
			dir := strings.TrimSpace(customEntry.Text)
			if dir == "" {
				cw.showError(errors.New("请填写或选择自定义目录"))
				return
			}
			cw.confirmLocation(dir, true)
		}
	})
	next.Importance = widget.HighImportance

	body := container.NewVBox(title, widget.NewLabel("配置文件 config.yaml 保存在："), radio, customRow, serviceWarn, hint)
	cw.setPage(body, widget.NewButton("取消", cw.cancel), next)
}

// asaCfgWouldChange 报告按当前选择完成向导后，用户级 ASA_CFG 会不会变（设上、改掉或删掉）。
func (cw *configWizard) asaCfgWouldChange(customText string) bool {
	newVal := ""
	if cw.loc == locCustom {
		newVal = customText
	}
	oldVal := ""
	if cw.userCfgSet {
		oldVal = cw.userCfg
	}
	if newVal == "" || oldVal == "" {
		return newVal != oldVal
	}
	return !fsutil.SamePath(newVal, oldVal)
}

// confirmLocation 校验第 1 页选定的目录；目录里已有 config.yaml 就直接接管（跳过第 2 页）。
func (cw *configWizard) confirmLocation(dir string, custom bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		cw.showError(err)
		return
	}
	if custom {
		// 映射盘符按登录会话挂载，LocalSystem 身份的服务看不到它：GUI 里能用、
		// 装成服务就找不到配置。
		if isNet, err := fsutil.IsNetworkDrive(abs); err == nil && isNet {
			cw.showError(fmt.Errorf("不能选择网络盘或网络共享目录：%s\n服务以系统账户运行时看不到映射的网络盘，请选本地磁盘目录", abs))
			return
		}
	}
	if err := os.MkdirAll(abs, 0o755); err != nil || !dirWritable(abs) {
		cw.showError(fmt.Errorf("目录不可写：%s，请换一个目录", abs))
		return
	}
	cw.cfgDir = abs

	existing := filepath.Join(abs, appconfig.ConfigFileName)
	if _, err := os.Stat(existing); err == nil {
		cw.path, cw.generated, cw.adopted = existing, true, true
		cw.applyAndReload(cw.showReviewPage)
		return
	}
	cw.showDataPage()
}

// ---------------------------------------------------------------- 第 2 页

func (cw *configWizard) showDataPage() {
	title := pageTitle("2/3  数据目录")
	desc := wrapLabel("ARK 服务端本体约 25GB，加上存档建议预留 30GB 以上。不能选网络盘（实例状态库依赖本地文件锁）。")

	dataEntry := widget.NewEntry()
	dataEntry.SetText(cw.cfgDir)
	browse := widget.NewButton("浏览…", nil)
	browse.OnTapped = func() { cw.pickFolder("选择数据目录", dataEntry, browse) }

	back := widget.NewButton("上一步", cw.showLocationPage)
	generate := widget.NewButton("生成配置", func() {
		data := strings.TrimSpace(dataEntry.Text)
		if data == "" {
			data = cw.cfgDir
		}
		abs, err := filepath.Abs(data)
		if err != nil {
			cw.showError(err)
			return
		}
		if err := appconfig.ValidateBaseDir(abs); err != nil {
			cw.showError(err)
			return
		}
		baseField := abs
		if fsutil.SamePath(abs, cw.cfgDir) {
			baseField = "" // 与配置同目录：basedir 留空，整个目录可以原样搬走
		}
		path, err := appconfig.InitConfig(appconfig.InitOptions{Dir: cw.cfgDir, BaseDir: baseField, Lang: appconfig.LangZH})
		if err != nil {
			cw.showError(err)
			return
		}
		cw.path, cw.generated = path, true
		cw.applyAndReload(cw.showReviewPage)
	})
	generate.Importance = widget.HighImportance

	body := container.NewVBox(title, desc, container.NewBorder(nil, nil, nil, browse, dataEntry))
	cw.setPage(body, back, widget.NewButton("取消", cw.cancel), generate)
}

// ---------------------------------------------------------------- 第 3 页

func (cw *configWizard) showReviewPage() {
	title := pageTitle("3/3  检查配置")
	head := "✓ 已生成 " + cw.path
	if cw.adopted {
		head = "✓ 已接管现有配置 " + cw.path
	}
	headLabel := wrapLabel(head)
	headLabel.Importance = widget.SuccessImportance

	lines := []fyne.CanvasObject{title, headLabel, wrapLabel("数据目录：" + cfgpkg.BaseDir)}
	if cw.envNote != "" {
		lines = append(lines, wrapLabel(cw.envNote))
	}
	lines = append(lines, wrapLabel("开始下载前建议确认：\n"+
		"  · download.github_proxy / http_proxy　下载代理（国内网络下载 SteamCMD 等）\n"+
		"  · server.port　管理面板端口（默认 19193）\n"+
		"  · server.tls / auth　HTTPS 与登录鉴权\n"+
		"用记事本改好并保存后，点「重新校验」确认无误。"))

	result := wrapLabel("")
	validate := func() {
		if _, err := appconfig.CheckFile(cw.path); err != nil {
			result.SetText("✗ 配置无法通过校验：" + err.Error())
			result.Importance = widget.DangerImportance
		} else {
			result.SetText("✓ 配置有效")
			result.Importance = widget.SuccessImportance
		}
		result.Refresh()
	}
	validate()

	notepad := widget.NewButton("用记事本打开", func() {
		// .yaml 在多数 Windows 上没有默认关联，走 ShellExecute 会弹「选择打开方式」；
		// 记事本所有版本都有，配合 BOM + CRLF 不会乱码、不会挤成一行。
		if err := exec.Command("notepad.exe", cw.path).Start(); err != nil {
			cw.showError(fmt.Errorf("打开记事本失败: %w", err))
		}
	})
	folder := widget.NewButton("打开所在文件夹", func() {
		// explorer 的 /select, 参数不能被整体加引号（Go 默认会给含空格的参数整体加），
		// 只能自己拼命令行。
		cmd := exec.Command("explorer.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + cw.path + `"`}
		_ = cmd.Start() // explorer 的退出码不可靠，不据此报错
	})
	recheck := widget.NewButton("重新校验", validate)
	lines = append(lines, container.NewHBox(notepad, folder, recheck), result)

	later := widget.NewButton("稍后再初始化", func() {
		cw.reloadThen(func() { cw.finish(false) })
	})
	start := widget.NewButton("保存并初始化环境 ▶", func() {
		cw.reloadThen(func() { cw.finish(true) })
	})
	start.Importance = widget.HighImportance
	cw.setPage(container.NewVBox(lines...), later, start)
}

// ---------------------------------------------------------------- 动作

// applyAndReload 在后台处理 ASA_CFG（写注册表 + 广播可能要几秒）并重新加载配置，
// 完成后回到 UI 线程执行 next。
func (cw *configWizard) applyAndReload(next func()) {
	cw.runBusy("正在应用配置…", func() error {
		cw.envNote = cw.applyEnvChoice()
		return cw.reload()
	}, func(err error) {
		if err != nil {
			cw.showError(err)
		}
		next()
	})
}

// reloadThen 重新加载配置（用户可能刚在记事本里改过），失败时回到第 3 页并弹出错误。
func (cw *configWizard) reloadThen(next func()) {
	cw.runBusy("正在加载配置…", cw.reload, func(err error) {
		if err != nil {
			cw.showReviewPage()
			cw.showError(err)
			return
		}
		next()
	})
}

// applyEnvChoice 按第 1 页的选择持久化 / 清理用户级 ASA_CFG，并同步当前进程，
// 返回给第 3 页看的说明。必须在配置文件写成功之后调用：反过来的话写失败会留下一个
// 指向空目录的环境变量，之后每次启动都找不到配置、都弹向导。
func (cw *configWizard) applyEnvChoice() string {
	if cw.lockedDir != "" {
		return ""
	}
	if cw.loc == locCustom {
		if err := userenv.SetUser("ASA_CFG", cw.cfgDir); err != nil {
			_ = os.Setenv("ASA_CFG", cw.cfgDir) // 至少本次运行能用
			return "⚠ 设置环境变量 ASA_CFG 失败：" + err.Error() + "\n下次启动将找不到这份配置，请手动设置 ASA_CFG=" + cw.cfgDir
		}
		_ = os.Setenv("ASA_CFG", cw.cfgDir)
		note := "已为当前用户设置环境变量 ASA_CFG=" + cw.cfgDir
		if cw.serviceInstalled {
			note += "\n已安装的服务仍使用安装时的位置，需要以管理员身份重新安装服务。"
		}
		return note
	}
	if cw.userCfgSet {
		// 改选程序目录 / 系统目录：用户级 ASA_CFG 排在查找顺序第一位，不删掉的话选了也不生效。
		if err := userenv.UnsetUser("ASA_CFG"); err != nil {
			return "⚠ 删除旧的环境变量 ASA_CFG 失败：" + err.Error() + "\n它会让程序继续读 " + cw.userCfg
		}
		_ = os.Unsetenv("ASA_CFG")
		return "已删除之前设置的用户环境变量 ASA_CFG（" + cw.userCfg + "）"
	}
	return ""
}

// reload 重新加载配置并应用到各运行时包与 GUI 内 API 服务要用的 webapi 变量。
func (cw *configWizard) reload() error {
	_, cfg, err := bootstrap.Reload()
	if err != nil {
		return err
	}
	webapi.ApplyConfig(cfg)
	return nil
}

// finish 关掉向导；startSetup 时接着打开环境初始化面板（§3.7），此时下载器已经
// 用上了用户在向导里改的代理。
func (cw *configWizard) finish(startSetup bool) {
	cw.busy = false
	cw.w.Close()
	cw.g.refreshEnvBanner()
	if startSetup {
		cw.g.showSetupProgress()
	}
}

// cancel 是第 1、2 页的「取消」与关窗。保留 §10.4 要求的退路：以默认值在程序目录
// （不可写时系统目录）生成配置，等价于旧版启动时的自动生成——否则下次启动还会弹
// 向导，而旧行为是「取消一次就不再打扰」。第 3 页（配置已落盘）关窗 = 稍后再初始化。
func (cw *configWizard) cancel() {
	if cw.busy {
		return
	}
	if cw.generated {
		cw.runBusy("正在加载配置…", cw.reload, func(err error) {
			cw.finish(false)
			if err != nil {
				cw.g.showError(err)
			}
		})
		return
	}
	dir := cw.exeDir
	switch {
	case cw.lockedDir != "":
		dir = cw.lockedDir
	case !cw.exeWritable && cw.sysDir != "":
		dir = cw.sysDir
	}
	cw.runBusy("正在生成默认配置…", func() error {
		if _, err := appconfig.InitConfig(appconfig.InitOptions{Dir: dir, Lang: appconfig.LangZH}); err != nil &&
			!errors.Is(err, appconfig.ErrConfigExists) {
			return err
		}
		return cw.reload()
	}, func(err error) {
		cw.finish(false)
		if err != nil {
			cw.g.showError(fmt.Errorf("生成默认配置失败：%w", err))
		}
	})
}

// runBusy 把页面换成「处理中」，在后台执行 work，完成后经 fyne.Do 回到 UI 线程调 done。
func (cw *configWizard) runBusy(msg string, work func() error, done func(error)) {
	cw.busy = true
	bar := widget.NewProgressBarInfinite()
	cw.w.SetContent(container.NewPadded(container.NewVBox(widget.NewLabel(msg), bar)))
	go func() {
		err := work()
		fyne.Do(func() {
			bar.Stop()
			cw.busy = false
			done(err)
		})
	}()
}

// pickFolder 打开系统原生的「选择文件夹」对话框，选中后填进 entry。
//
// 原生对话框是模态阻塞调用，放在独立 goroutine（folderpicker 自己锁 OS 线程、初始化
// STA），不占 Fyne 主线程——否则主窗口会「未响应」。owner 在点击时取前台窗口（此刻
// 必然是向导），保证对话框浮在向导之上。原生对话框用不了时回退到 Fyne 自绘的。
func (cw *configWizard) pickFolder(title string, entry *widget.Entry, btn *widget.Button) {
	owner := folderpicker.ForegroundWindow()
	initial := strings.TrimSpace(entry.Text)
	btn.Disable()
	go func() {
		path, ok, err := folderpicker.Pick(title, initial, owner)
		fyne.Do(func() {
			btn.Enable()
			switch {
			case err != nil:
				fd := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
					if err == nil && uri != nil {
						entry.SetText(uri.Path())
					}
				}, cw.w)
				fd.SetConfirmText("选择此目录")
				fd.Show()
			case ok:
				entry.SetText(path)
			}
		})
	}()
}

// ---------------------------------------------------------------- 布局小工具

func (cw *configWizard) setPage(body fyne.CanvasObject, buttons ...fyne.CanvasObject) {
	// 左侧一个 spacer 把按钮挤到右边。
	bar := container.NewHBox(append([]fyne.CanvasObject{layout.NewSpacer()}, buttons...)...)
	cw.w.SetContent(container.NewPadded(container.NewBorder(nil, bar, nil, nil, container.NewVScroll(body))))
}

func (cw *configWizard) showError(err error) { dialog.ShowError(err, cw.w) }

func pageTitle(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

func wrapLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// dirWritable 探测当前用户能否在 dir 里建文件（不存在返回 false）。
func dirWritable(dir string) bool {
	if dir == "" {
		return false
	}
	f, err := os.CreateTemp(dir, ".asa-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}
