package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	// 以下为你项目内依赖，确保这些包在你的工程存在
	"asa-server/asaserver"
	"asa-server/backup"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ----------------------------- 公共类型与工具 -----------------------------

type ModelType int

const (
	InstanceSelectModel ModelType = iota
	InstanceManageModel
	BackupSelectModel
	RCONInputModel
	MessageModel
	LogViewerModel
)

const gap = "\n\n"

type InstanceListView struct {
	list      list.Model
	instances []string
}

func NewInstanceListView(width, height int) *InstanceListView {
	l := list.New([]list.Item{}, list.NewDefaultDelegate(), width, height)
	l.Title = "选择实例"
	return &InstanceListView{list: l}
}

func (v *InstanceListView) SetInstances(instances []string) {
	v.instances = instances
	items := make([]list.Item, len(instances))
	for i, inst := range instances {
		running, _ := asaserver.IsServerRunning(inst)
		info, _ := asaserver.LoadInstanceConfig(inst)
		status := "[离线]"
		if running {
			status = "[在线]"
		}
		desc := fmt.Sprintf("%s|%s", info.ServerName, status)
		items[i] = listItem{title: inst, description: desc}
	}
	v.list.SetItems(items)
}

func (v *InstanceListView) Init() tea.Cmd { return nil }

func (v *InstanceListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	lm, cmd := v.list.Update(msg)
	v.list = lm
	return v, cmd
}

func (v *InstanceListView) View() string {
	return v.list.View()
}

func (v *InstanceListView) SetSize(w, h int) {
	v.list.SetSize(w, h-3)
}

// ----------------------------- 组件：ManageListView -----------------------------

type ManageListView struct {
	list list.Model
}

func NewManageListView(width, height int) *ManageListView {
	l := list.New([]list.Item{}, itemDelegate{}, width, height)
	l.Title = "管理菜单"
	return &ManageListView{list: l}
}

func (m *ManageListView) SetOptions(opts []string) {
	items := make([]list.Item, len(opts))
	for i, o := range opts {
		items[i] = listItem{title: o, description: ""}
	}
	m.list.SetItems(items)
}

func (m *ManageListView) Init() tea.Cmd { return nil }

func (m *ManageListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	lm, cmd := m.list.Update(msg)
	m.list = lm
	return m, cmd
}

func (m *ManageListView) View() string { return m.list.View() }

func (m *ManageListView) SetSize(w, h int) {
	m.list.SetSize(w, h-3)
}

// ----------------------------- 组件：BackupListView -----------------------------

type BackupListView struct {
	list  list.Model
	paths []string
}

func (b *BackupListView) Init() tea.Cmd {
	return nil
}

func NewBackupListView(width, height int) *BackupListView {
	l := list.New([]list.Item{}, itemDelegate{}, width, height)
	l.Title = "选择备份"
	return &BackupListView{list: l}
}

func (b *BackupListView) SetBackups(paths []string) {
	b.paths = paths
	items := make([]list.Item, len(paths))
	for i, p := range paths {
		items[i] = listItem{title: filepath.Base(p), description: ""}
	}
	b.list.SetItems(items)
}

func (b *BackupListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	lm, cmd := b.list.Update(msg)
	b.list = lm
	return b, cmd
}

func (b *BackupListView) View() string { return b.list.View() }

func (b *BackupListView) SetSize(w, h int) {
	b.list.SetSize(w, h-3)
}

// ----------------------------- 组件：RCONView -----------------------------

type RCONView struct {
	viewport  viewport.Model
	textarea  textarea.Model
	messages  []string
	senderSty lipgloss.Style
}

func NewRCONView(w, h int) *RCONView {
	ta := textarea.New()
	ta.Placeholder = "输入命令..."
	ta.Prompt = "┃ "
	ta.CharLimit = 1024
	ta.SetWidth(w)
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(w, h-3)
	vp.SetContent("欢迎进入RCON终端")
	return &RCONView{
		viewport:  vp,
		textarea:  ta,
		messages:  []string{},
		senderSty: lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
	}
}

func (r *RCONView) Init() tea.Cmd {
	r.messages = []string{
		"欢迎进入RCON终端！",
		"输入命令并按 Enter 发送，按 Ctrl+C 退出。",
	}
	r.textarea.Reset()
	r.viewport.SetContent(strings.Join(r.messages, "\n"))
	r.viewport.GotoBottom()
	r.textarea.Focus()
	return nil
}

func (r *RCONView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var taCmd tea.Cmd
	var vpCmd tea.Cmd
	r.textarea, taCmd = r.textarea.Update(msg)
	r.viewport, vpCmd = r.viewport.Update(msg)

	// 处理回车发送命令逻辑需要在外层 Update 调用时再触发（因为需要调用 asaserver.SendRCONCommand）
	return r, tea.Batch(taCmd, vpCmd)
}

func (r *RCONView) View() string {
	return fmt.Sprintf("%s%s%s",
		r.viewport.View(),
		gap,
		r.textarea.View())
}

func (r *RCONView) SetSize(w, h int) {
	r.viewport.Width = w
	r.textarea.SetWidth(w)
	r.textarea.SetHeight(3)
	r.viewport.Height = h - r.textarea.Height() - lipgloss.Height(gap)
}

// ----------------------------- 组件：LogViewer -----------------------------

type LogViewer struct {
	viewport viewport.Model
	lines    []string
	spinner  spinner.Model
	logCh    chan string
	stopChan chan bool
	reading  bool
	width    int
	height   int
	maxLines int
	throttle time.Duration // 节流时长，合并多行再刷新，减少重绘
	lastLen  int
	initDone bool
}

func NewLogViewer(w, h int) *LogViewer {
	vp := viewport.New(w, h-3)
	s := spinner.New()
	s.Spinner = spinner.Dot
	return &LogViewer{
		viewport: vp,
		spinner:  s,
		lines:    []string{},
		logCh:    nil,
		stopChan: nil,
		reading:  false,
		width:    w,
		height:   h,
		maxLines: 2000,
		throttle: 100 * time.Millisecond,
	}
}

// Start 开始监听日志文件。返回一个 tea.Cmd（用于第一次把 waitForLogCmd 注册到 runtime 中）或 error
func (lv *LogViewer) Start(logPath string) (tea.Cmd, error) {
	// init content
	lv.lines = []string{
		fmt.Sprintf("日志文件: %s", logPath),
		"实时监听日志中...",
		"",
	}
	lv.reading = true
	lv.logCh = make(chan string, 2048)
	lv.stopChan = make(chan bool, 1)
	lv.lastLen = len(lv.lines)
	lv.initDone = false

	// 启动 tail goroutine（假定 asaserver.TailLogFile(path, func(line string) {}) -> stop func()）
	go func() {
		// asaserver.TailLogFile 按你之前用法：返回 stop func()
		stop := asaserver.TailLogFile(logPath, func(line string) {
			// 若已停止，尽快返回
			select {
			case <-lv.stopChan:
				return
			default:
			}
			// 非阻塞写入 channel
			select {
			case lv.logCh <- line:
			default:
				// channel 满了，丢弃当前行以免阻塞
			}
		})

		// 等待 stop 信号
		<-lv.stopChan
		stop()
		// 关闭 logCh 以通知 Consumer 结束
		close(lv.logCh)
	}()

	// 初始把已有行写到 viewport
	lv.viewport.SetContent(strings.Join(lv.lines, "\n"))
	lv.viewport.GotoBottom()
	// 返回首次注册的 Cmd（会开始循环读取 logCh）
	return waitForLogCmd(lv.logCh), nil
}

func (lv *LogViewer) Stop() {
	if !lv.reading {
		return
	}
	lv.reading = false
	// 发送停止信号（非阻塞）
	select {
	case lv.stopChan <- true:
	default:
	}
}

func (lv *LogViewer) SetSize(w, h int) {
	lv.width = w
	lv.height = h
	lv.viewport.Width = w
	lv.viewport.Height = h - 3
}

// AppendLines 由 Update 在接收到 LogMsg 后调用：把行追加到内部缓存，并更新 viewport
func (lv *LogViewer) AppendLines(chunk string) {
	// chunk 可能是多行（因节流合并），按行切割
	parts := strings.Split(chunk, "\n")
	for _, p := range parts {
		lv.lines = append(lv.lines, p)
	}
	trimLines(&lv.lines, lv.maxLines)
	// 更新 viewport 内容并滚动到底部
	lv.viewport.SetContent(strings.Join(lv.lines, "\n"))
	lv.viewport.GotoBottom()
	lv.lastLen = len(lv.lines)
}

func (lv *LogViewer) Init() tea.Cmd {
	return nil
}

func (lv *LogViewer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	lv.viewport, cmd = lv.viewport.Update(msg)
	return lv, cmd
}

func (lv *LogViewer) View() string {
	return lv.viewport.View()
}

// ----------------------------- AppModel: 主模型 -----------------------------

type AppModel struct {
	modelStack   []ModelType
	currentModel ModelType
	width        int
	height       int

	// 组件
	instanceView *InstanceListView
	manageView   *ManageListView
	backupView   *BackupListView
	rconView     *RCONView
	logViewer    *LogViewer
	// 业务状态
	managingInstance string
	messageTitle     string
	messageBody      string
	messageType      string // "info","error","success"
	shouldExit       bool
}

func NewModel() *AppModel {
	w, h := 80, 24 // 初始尺寸（后续 WindowSizeMsg 会更新）
	inst := NewInstanceListView(w, h)
	mg := NewManageListView(w, h)
	bk := NewBackupListView(w, h)
	rv := NewRCONView(w, h)
	lv := NewLogViewer(w, h)

	am := &AppModel{
		modelStack:       []ModelType{},
		currentModel:     InstanceSelectModel,
		instanceView:     inst,
		manageView:       mg,
		backupView:       bk,
		rconView:         rv,
		logViewer:        lv,
		managingInstance: "",
	}
	// 加载实例
	instances, _ := asaserver.GetAvailableInstances()
	am.instanceView.SetInstances(instances)
	return am
}

func (a *AppModel) Init() tea.Cmd { return nil }

func (a *AppModel) pushModel(t ModelType) {
	a.modelStack = append(a.modelStack, a.currentModel)
	a.currentModel = t
}

func (a *AppModel) popModel() {
	if len(a.modelStack) > 0 {
		a.currentModel = a.modelStack[len(a.modelStack)-1]
		a.modelStack = a.modelStack[:len(a.modelStack)-1]
	}
}

func (a *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch mt := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = mt.Width
		a.height = mt.Height
		// 调整组件尺寸
		a.instanceView.SetSize(a.width, a.height)
		a.manageView.SetSize(a.width, a.height)
		a.backupView.SetSize(a.width, a.height)
		a.rconView.SetSize(a.width, a.height)
		a.logViewer.SetSize(a.width, a.height)
		// 继续让当前 model 也能接收到 WindowSizeMsg（下面会转发）
	case tea.KeyMsg:
		handled, cmd := a.handleKeyPress(mt)
		if handled {
			return a, cmd
		}
		// 如果没有被 handleKeyPress 消耗，继续让当前 model 处理该按键（不要在这里直接返回）
	case LogMsg:
		// 日志消息（可能是多行合并块）
		if a.logViewer != nil && a.currentModel == LogViewerModel {
			a.logViewer.AppendLines(string(mt))
			// 继续监听
			return a, waitForLogCmd(a.logViewer.logCh)
		}
		return a, nil
	}

	// 把消息分发给当前组件（组件自己负责处理上下键、过滤、Esc 等）
	switch a.currentModel {
	case InstanceSelectModel:
		_, cmd := a.instanceView.Update(msg)
		return a, cmd

	case InstanceManageModel:
		// 如果是 Enter —— 先拿选择项并执行（保持原有逻辑）
		if km, ok := msg.(tea.KeyMsg); ok && km.String() == "enter" {
			selected := a.manageView.list.SelectedItem()
			if selected != nil {
				item := selected.(listItem)
				cmd := a.executeManageOption(item.title)
				_, listCmd := a.manageView.Update(msg)
				if cmd != nil {
					return a, tea.Batch(listCmd, cmd)
				}
				return a, listCmd
			}
		}
		// 其它按键交给 manageView
		_, cmd := a.manageView.Update(msg)
		return a, cmd

	case BackupSelectModel:
		// 备份选择时 Enter 执行恢复
		if km, ok := msg.(tea.KeyMsg); ok && km.String() == "enter" {
			selected := a.backupView.list.SelectedItem()
			if selected != nil {
				item := selected.(listItem)
				for _, p := range a.backupView.paths {
					if filepath.Base(p) == item.title {
						err := backup.RestoreBackupToInstance(a.managingInstance, p, backup.WithRestoreAll())
						if err != nil {
							a.showMessage("恢复失败", fmt.Sprintf("错误: %v", err), "error")
						} else {
							a.showMessage("成功", "备份恢复成功", "success")
						}
						break
					}
				}
				a.popModel()
			}
		}
		_, cmd := a.backupView.Update(msg)
		return a, cmd

	case RCONInputModel:
		_, _ = a.rconView.Update(msg)
		if km, ok := msg.(tea.KeyMsg); ok && km.Type == tea.KeyEnter {
			command := a.rconView.textarea.Value()
			if command != "" {
				a.rconView.messages = append(a.rconView.messages, a.rconView.senderSty.Render("You: ")+command)
				resp, err := asaserver.SendRCONCommand(a.managingInstance, command)
				if err != nil {
					a.rconView.messages = append(a.rconView.messages, "Error: "+fmt.Sprintf("%v", err))
				} else {
					a.rconView.messages = append(a.rconView.messages, "Server: "+resp)
				}
				a.rconView.viewport.SetContent(lipgloss.NewStyle().Width(a.rconView.viewport.Width).Render(strings.Join(a.rconView.messages, "\n")))
				a.rconView.viewport.GotoBottom()
				a.rconView.textarea.Reset()
			}
		}
		return a, nil
	case MessageModel:
		if km, ok := msg.(tea.KeyMsg); ok {
			if km.String() == "enter" || km.String() == " " {
				a.popModel()
			}
		}
		return a, nil

	case LogViewerModel:
		_, cmd := a.logViewer.Update(msg)
		return a, cmd
	}

	return a, nil
}

func (a *AppModel) View() string {
	if a.shouldExit {
		return ""
	}
	switch a.currentModel {
	case InstanceSelectModel:
		return a.instanceView.View()
	case InstanceManageModel:
		return a.manageView.View()
	case BackupSelectModel:
		return a.backupView.View()
	case RCONInputModel:
		// 渲染 RCON viewport + textarea
		return fmt.Sprintf("%s%s%s", a.rconView.viewport.View(), gap, a.rconView.textarea.View())
	case MessageModel:
		content := "=== " + a.messageTitle + " ===\n\n"
		content += a.messageBody + "\n"
		content += "\n按 Enter 或空格继续..."
		return content
	case LogViewerModel:
		// 当 logViewer 有新内容时，AppendLines 已经更新 viewport
		helpText := "\n↑↓ 滑动  Ctrl+C/Esc 退出"
		return fmt.Sprintf("%s\n%s\n%s", "=== 实时日志 ===", a.logViewer.View(), helpText)
	}
	return ""
}

// ----------------------------- 高阶行为函数 -----------------------------

func getManageOptions() []string {
	return []string{
		"启动服务器",
		"停止服务器",
		"重启服务器",
		"查看状态",
		"发送RCON命令",
		"备份世界",
		"查看日志",
		"返回",
	}
}

func (a *AppModel) showMessage(title, body, msgType string) {
	a.messageTitle = title
	a.messageBody = body
	a.messageType = msgType
	a.pushModel(MessageModel)
}

// executeManageOption 返回可能的 tea.Cmd（例如查看日志会返回 waitForLogCmd）
func (a *AppModel) executeManageOption(option string) tea.Cmd {
	switch option {
	case "启动服务器":
		if err := asaserver.StartServer(a.managingInstance); err != nil {
			a.showMessage("启动失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			a.showMessage("成功", fmt.Sprintf("实例 '%s' 启动成功", a.managingInstance), "success")
		}
	case "停止服务器":
		if err := asaserver.StopServer(a.managingInstance); err != nil {
			a.showMessage("停止失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			a.showMessage("成功", fmt.Sprintf("实例 '%s' 已停止", a.managingInstance), "success")
		}
	case "重启服务器":
		if err := asaserver.RestartServer(a.managingInstance); err != nil {
			a.showMessage("重启失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			a.showMessage("成功", fmt.Sprintf("实例 '%s' 重启成功", a.managingInstance), "success")
		}
	case "查看状态":
		running, err := asaserver.IsServerRunning(a.managingInstance)
		if err != nil {
			a.showMessage("错误", fmt.Sprintf("查询状态失败: %v", err), "error")
		} else {
			status := "离线"
			if running {
				status = "在线"
			}
			a.showMessage("实例状态", fmt.Sprintf("实例 '%s' 当前状态: %s", a.managingInstance, status), "info")
		}
	case "发送RCON命令":
		a.rconView.Init()
		a.pushModel(RCONInputModel)
	case "备份世界":
		if err := backup.BackupInstanceWorld(a.managingInstance); err != nil {
			a.showMessage("备份失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			a.showMessage("成功", fmt.Sprintf("实例 '%s' 备份成功", a.managingInstance), "success")
		}
	case "查看日志":
		// 先检查实例是否在运行并拿到日志路径
		running, err := asaserver.IsServerRunning(a.managingInstance)
		if err != nil {
			a.showMessage("错误", err.Error(), "error")
			return nil
		}
		if !running {
			a.showMessage("错误", "实例未在运行中", "error")
			return nil
		}
		logPath, exists := asaserver.GetInstanceLogFile(a.managingInstance)
		if !exists {
			logPath, err = asaserver.GetGameLogFilePath(a.managingInstance)
			if err != nil {
				a.showMessage("错误", fmt.Sprintf("获取日志文件路径失败: %v", err), "error")
				return nil
			}
		}
		// 启动 logViewer 并返回第一次要注册到 runtime 的 cmd（waitForLogCmd）
		cmd, err := a.logViewer.Start(logPath)
		if err != nil {
			a.showMessage("错误", fmt.Sprintf("启动日志查看失败: %v", err), "error")
			return nil
		}
		a.pushModel(LogViewerModel)
		return cmd
	case "返回":
		a.popModel()
	}
	return nil
}

func (a *AppModel) handleKeyPress(km tea.KeyMsg) (bool, tea.Cmd) {
	switch km.String() {
	case "ctrl+c":
		// 立即退出（保留原行为）
		if a.currentModel == LogViewerModel {
			a.logViewer.Stop()
			a.popModel()
			return true, nil
		}
		a.shouldExit = true
		return true, tea.Quit
	}

	// 首页（InstanceSelectModel）：把按键全部交给 list 处理（包括上下、enter、esc 等）
	if a.currentModel == InstanceSelectModel {
		if km.String() == "enter" {
			sel := a.instanceView.list.SelectedItem()
			if sel != nil {
				it := sel.(listItem)
				a.managingInstance = it.title
				a.manageView.list.Title = fmt.Sprintf("管理菜单(%s)", it.title)
				a.manageView.SetOptions(getManageOptions())
				a.pushModel(InstanceManageModel)
			}
			return true, nil
		}
		_, cmd := a.instanceView.Update(km)
		return true, cmd
	}

	// 特别处理 Esc：先交给组件，如果组件没有处理，再决定是否 pop（只有在有栈可 pop 时才弹栈）
	if km.String() == "esc" {
		switch a.currentModel {
		case InstanceManageModel:
			// 转发 esc 给组件
			_, cmd := a.manageView.Update(km)

			// 若组件返回了 cmd，认为已处理
			if cmd != nil {
				return true, cmd
			}

			if a.manageView.list.FilteringEnabled() {
				return true, nil
			}

			// 组件没有处理，且有可 pop 的 model，则弹栈；否则（已在根）**吞掉 Esc，不退出程序**
			if len(a.modelStack) > 0 {
				a.popModel()
			}
			// 无论是否有栈，都把 Esc 当作已消费（避免传到别处导致退出）
			return true, nil

		case BackupSelectModel:
			_, cmd := a.backupView.Update(km)
			if cmd != nil {
				return true, cmd
			}
			if len(a.modelStack) > 0 {
				a.popModel()
			}
			return true, nil

		case RCONInputModel:
			_, cmd := a.rconView.Update(km)
			if cmd != nil {
				return true, cmd
			}
			if len(a.modelStack) > 0 {
				a.popModel()
			}
			return true, nil

		case LogViewerModel:
			_, cmd := a.logViewer.Update(km)
			if cmd != nil {
				return true, cmd
			}
			// 如果 logViewer 未消费，则 stop 并 pop（因为通常 Esc 在日志视图是返回上层）
			a.logViewer.Stop()
			if len(a.modelStack) > 0 {
				a.popModel()
			}
			return true, nil

		default:
			// 其它模式：若有栈就 pop；若没有栈则吞掉（不退出程序）
			if len(a.modelStack) > 0 {
				a.popModel()
			}
			return true, nil
		}
	}

	// 其它按键未在这里消费，交给后续 per-model 分发
	return false, nil
}
