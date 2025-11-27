package tui

import (
	"asa-server/asaserver"
	"asa-server/backup"
	"asa-server/logger"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// listItem 列表项目 - 定义住 common.go

// 模型类型
type ModelType int

const (
	InstanceSelectModel ModelType = iota
	InstanceManageModel
	BackupSelectModel
	RCONInputModel
	RenameInputModel
	MessageModel
	LogViewerModel
)

const gap = "\n\n"

// Model 是应用主模型
type Model struct {
	modelStack    []ModelType
	currentModel  ModelType
	width, height int

	// 实例列表
	instanceList list.Model
	instances    []string

	// 管理菜单列表
	manageList       list.Model
	managingInstance string
	manageOptions    []string

	// 备份列表
	backupList        list.Model
	availableBackups  []string
	selectedBackupIdx int

	// RCON 聊天式交互
	rconViewport  viewport.Model
	rconTextarea  textarea.Model
	rconMessages  []string
	rconConnected bool
	rconSender    lipgloss.Style

	// 日志查看
	logViewport viewport.Model
	logLines    []string
	logSpinner  spinner.Model
	logStopChan chan bool
	logReading  bool
	logInitDone bool
	logLastLen  int

	// 输入状态
	inputValue    string
	inputLabel    string
	inputCallback func(string) error

	// 消息显示
	messageTitle string
	messageBody  string
	messageType  string // "info", "error", "success"

	// 退出标志
	shouldExit bool
}

// Init 初始化模型
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update 处理消息更新
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateListSize()
		if m.currentModel == RCONInputModel {
			m.updateRCONSize()
		}
		if m.currentModel == LogViewerModel {
			m.updateLogViewerSize()
		}
	}
	return m, nil
}

// View 渲染视图
func (m *Model) View() string {
	if m.shouldExit {
		return ""
	}
	switch m.currentModel {
	case InstanceSelectModel:
		return m.viewInstanceSelect()
	case InstanceManageModel:
		return m.viewInstanceManage()
	case BackupSelectModel:
		return m.viewBackupSelect()
	case RCONInputModel:
		return m.viewRCONInput()
	case RenameInputModel:
		return m.viewRenameInput()
	case MessageModel:
		return m.viewMessage()
	case LogViewerModel:
		return m.viewLogViewer()
	}
	return ""
}

// handleKeyPress 处理按键
func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.currentModel == MessageModel || m.currentModel == RenameInputModel {
			m.popModel()
			return m, nil
		}
		if m.currentModel == RCONInputModel {
			m.popModel()
			return m, nil
		}
		if m.currentModel == LogViewerModel {
			m.stopLogViewer()
			m.popModel()
			return m, nil
		}
		m.shouldExit = true
		return m, tea.Quit
	case "esc":
		if len(m.modelStack) > 1 {
			if m.currentModel == LogViewerModel {
				m.stopLogViewer()
			}
			m.popModel()
			return m, nil
		}
	}

	switch m.currentModel {
	case InstanceSelectModel:
		return m.handleInstanceSelectKeys(msg)
	case InstanceManageModel:
		return m.handleInstanceManageKeys(msg)
	case BackupSelectModel:
		return m.handleBackupSelectKeys(msg)
	case RCONInputModel:
		return m.handleRCONInputKeys(msg)
	case RenameInputModel:
		return m.handleRenameInputKeys(msg)
	case MessageModel:
		return m.handleMessageKeys(msg)
	case LogViewerModel:
		return m.handleLogViewerKeys(msg)
	}
	return m, nil
}

// handleInstanceSelectKeys 处理实例选择按键
func (m *Model) handleInstanceSelectKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		selectedItem := m.instanceList.SelectedItem()
		if selectedItem != nil {
			item := selectedItem.(listItem)
			m.managingInstance = item.title
			m.manageList.Title = fmt.Sprintf("管理菜单(%s)", item.title)
			m.initManageList()
			m.pushModel(InstanceManageModel)
		}
	}

	var cmd tea.Cmd
	m.instanceList, cmd = m.instanceList.Update(msg)
	return m, cmd
}

// handleInstanceManageKeys 处理实例管理菜单按键
func (m *Model) handleInstanceManageKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		selectedItem := m.manageList.SelectedItem()
		if selectedItem != nil {
			item := selectedItem.(listItem)
			m.executeManageOption(item.title)
		}
	}

	var cmd tea.Cmd
	m.manageList, cmd = m.manageList.Update(msg)
	return m, cmd
}

// initManageList 初始化管理菜单列表
func (m *Model) initManageList() {
	options := getManageOptions()
	items := make([]list.Item, len(options))
	for i, opt := range options {
		items[i] = listItem{title: opt, description: ""}
	}
	m.manageList.SetItems(items)
}

// initInstanceList 初始化实例列表
func (m *Model) initInstanceList() {
	items := make([]list.Item, len(m.instances))
	for i, inst := range m.instances {
		running, _ := asaserver.IsServerRunning(inst)
		info, _ := asaserver.LoadInstanceConfig(inst)
		status := "[离线]"
		if running {
			status = "[在线]"
		}
		description := fmt.Sprintf("%s|%s", info.ServerName, status)
		items[i] = listItem{title: inst, description: description}
	}
	m.instanceList.SetItems(items)
}

// initBackupList 初始化备份列表
func (m *Model) initBackupList() {
	items := make([]list.Item, len(m.availableBackups))
	for i, backup := range m.availableBackups {
		filename := filepath.Base(backup)
		items[i] = listItem{title: filename, description: ""}
	}
	m.backupList.SetItems(items)
}

// updateListSize 更新列表大小
func (m *Model) updateListSize() {
	m.instanceList.SetSize(m.width, m.height-3)
	m.manageList.SetSize(m.width, m.height-3)
	m.backupList.SetSize(m.width, m.height-3)
}

// updateRCONSize 更新RCON窗口大小
func (m *Model) updateRCONSize() {
	m.rconViewport.Width = m.width
	m.rconTextarea.SetWidth(m.width)
	m.rconViewport.Height = m.height - m.rconTextarea.Height() - lipgloss.Height(gap)

	if len(m.rconMessages) > 0 {
		m.rconViewport.SetContent(lipgloss.NewStyle().Width(m.rconViewport.Width).Render(strings.Join(m.rconMessages, "\n")))
	}
	m.rconViewport.GotoBottom()
}

// updateLogViewerSize 更新日志查看器大小
func (m *Model) updateLogViewerSize() {
	m.logViewport.Width = m.width
	m.logViewport.Height = m.height - 3
}

// executeManageOption 执行管理选项
func (m *Model) executeManageOption(option string) {
	switch option {
	case "启动服务器":
		err := asaserver.StartServer(m.managingInstance)
		if err != nil {
			m.showMessage("启动失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			m.showMessage("成功", fmt.Sprintf("实例 '%s' 启动成功", m.managingInstance), "success")
		}

	case "停止服务器":
		err := asaserver.StopServer(m.managingInstance)
		if err != nil {
			m.showMessage("停止失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			m.showMessage("成功", fmt.Sprintf("实例 '%s' 已停止", m.managingInstance), "success")
		}

	case "重启服务器":
		err := asaserver.RestartServer(m.managingInstance)
		if err != nil {
			m.showMessage("重启失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			m.showMessage("成功", fmt.Sprintf("实例 '%s' 重启成功", m.managingInstance), "success")
		}

	case "查看状态":
		running, err := asaserver.IsServerRunning(m.managingInstance)
		if err != nil {
			m.showMessage("错误", fmt.Sprintf("查询状态失败: %v", err), "error")
		} else {
			status := "离线"
			if running {
				status = "在线"
			}
			m.showMessage("实例状态", fmt.Sprintf("实例 '%s' 当前状态: %s", m.managingInstance, status), "info")
		}

	case "发送RCON命令":
		m.initRCONChat()
		m.pushModel(RCONInputModel)

	case "备份世界":
		err := backup.BackupInstanceWorld(m.managingInstance)
		if err != nil {
			m.showMessage("备份失败", fmt.Sprintf("错误: %v", err), "error")
		} else {
			m.showMessage("成功", fmt.Sprintf("实例 '%s' 备份成功", m.managingInstance), "success")
		}

	case "恢复备份":
		backups, err := backup.GetAvailableBackups()
		if err != nil {
			m.showMessage("错误", fmt.Sprintf("加载备份失败: %v", err), "error")
		} else if len(backups) == 0 {
			m.showMessage("提示", "没有可用的备份", "info")
		} else {
			m.availableBackups = backups
			m.initBackupList()
			m.pushModel(BackupSelectModel)
		}
	case "查看日志":
		m.startLogViewer()
	case "编辑配置":
		m.editInstanceConfig()
	case "重命名实例":
		m.inputLabel = "请输入新的实例名称:"
		m.inputValue = m.managingInstance
		m.inputCallback = func(newName string) error {
			if newName == "" {
				m.showMessage("错误", "实例名称不能为空", "error")
				return nil
			}
			if newName == m.managingInstance {
				m.showMessage("提示", "新名称与旧名称相同", "info")
				return nil
			}
			return m.renameInstance(newName)
		}
		m.pushModel(RenameInputModel)

	case "返回":
		m.popModel()
	}
}

// initRCONChat 初始化RCON聊天
func (m *Model) initRCONChat() {
	m.rconMessages = []string{
		"欢迎进入RCON终端！",
		"输入命令并按 Enter 发送，按 Ctrl+C 退出。",
	}
	m.rconTextarea.Reset()
	m.rconViewport.SetContent(strings.Join(m.rconMessages, "\n"))
	m.rconViewport.GotoBottom()
	m.rconTextarea.Focus()
	m.updateRCONSize()
}

// viewInstanceLogs 查看实例日志
func (m *Model) viewInstanceLogs() {
	logPath, exists := asaserver.GetInstanceLogFile(m.managingInstance)
	if !exists {
		var err error
		logPath, err = asaserver.GetGameLogFilePath(m.managingInstance)
		if err != nil {
			m.showMessage("错误", fmt.Sprintf("获取日志文件路径失败: %v", err), "error")
			return
		}
	}

	content := fmt.Sprintf("日志文件: %s\n\n(在生产环境中应打开外部查看器)", logPath)
	logger.GetLogger().Infof("日志路径: %s", logPath)
	m.showMessage("日志查看", content, "info")
}

// editInstanceConfig 编辑实例配置
func (m *Model) editInstanceConfig() {
	configPath := filepath.Join(asaserver.InstancesDir, m.managingInstance, "instance_config.ini")

	cmd := exec.Command("notepad.exe", configPath)
	if err := cmd.Run(); err != nil {
		m.showMessage("错误", fmt.Sprintf("打开记事本失败: %v", err), "error")
		return
	}

	m.showMessage("成功", "配置文件编辑完成", "success")
}

// renameInstance 重命名实例
func (m *Model) renameInstance(newName string) error {
	if running, _ := asaserver.IsServerRunning(m.managingInstance); running {
		if err := asaserver.StopServer(m.managingInstance); err != nil {
			m.showMessage("错误", fmt.Sprintf("停止实例失败: %v", err), "error")
			return nil
		}
	}

	oldPath := filepath.Join(asaserver.InstancesDir, m.managingInstance)
	newPath := filepath.Join(asaserver.InstancesDir, newName)

	if err := renameWithFallback(oldPath, newPath); err != nil {
		m.showMessage("错误", fmt.Sprintf("重命名失败: %v", err), "error")
		return nil
	}

	config, err := asaserver.LoadInstanceConfig(newName)
	if err == nil {
		config.SaveDir = newName
		asaserver.SaveInstanceConfig(newName, config)
	}

	m.managingInstance = newName
	m.showMessage("成功", fmt.Sprintf("实例已重命名为 '%s'", newName), "success")
	return nil
}

// handleBackupSelectKeys 处理备份选择按键
func (m *Model) handleBackupSelectKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		selectedItem := m.backupList.SelectedItem()
		if selectedItem != nil {
			item := selectedItem.(listItem)
			for _, backupPath := range m.availableBackups {
				if filepath.Base(backupPath) == item.title {
					err := backup.RestoreBackupToInstance(m.managingInstance, backupPath, backup.WithRestoreAll())
					if err != nil {
						m.showMessage("恢复失败", fmt.Sprintf("错误: %v", err), "error")
					} else {
						m.showMessage("成功", fmt.Sprintf("备份恢复成功"), "success")
					}
					break
				}
			}
			m.popModel()
		}
	}

	var cmd tea.Cmd
	m.backupList, cmd = m.backupList.Update(msg)
	return m, cmd
}

// handleRCONInputKeys 处理RCON输入按键
func (m *Model) handleRCONInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.rconTextarea, taCmd = m.rconTextarea.Update(msg)
	m.rconViewport, vpCmd = m.rconViewport.Update(msg)

	switch msg.Type {
	case tea.KeyEnter:
		command := m.rconTextarea.Value()
		if command != "" {
			m.rconMessages = append(m.rconMessages, m.rconSender.Render("You: ")+command)
			m.rconViewport.SetContent(lipgloss.NewStyle().Width(m.rconViewport.Width).Render(strings.Join(m.rconMessages, "\n")))
			m.rconViewport.GotoBottom()

			response, err := asaserver.SendRCONCommand(m.managingInstance, command)
			if err != nil {
				m.rconMessages = append(m.rconMessages, "Error: "+fmt.Sprintf("%v", err))
			} else {
				m.rconMessages = append(m.rconMessages, "Server: "+response)
			}
			m.rconViewport.SetContent(lipgloss.NewStyle().Width(m.rconViewport.Width).Render(strings.Join(m.rconMessages, "\n")))
			m.rconViewport.GotoBottom()

			m.rconTextarea.Reset()
		}
	}

	return m, tea.Batch(taCmd, vpCmd)
}

// handleRenameInputKeys 处理重命名输入按键
func (m *Model) handleRenameInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.inputCallback != nil {
			m.inputCallback(m.inputValue)
		}
		m.popModel()
	case "backspace":
		if len(m.inputValue) > 0 {
			m.inputValue = m.inputValue[:len(m.inputValue)-1]
		}
	default:
		if len(msg.String()) == 1 && msg.Runes != nil {
			m.inputValue += string(msg.Runes[0])
		}
	}
	return m, nil
}

// handleMessageKeys 处理消息按键
func (m *Model) handleMessageKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", " ":
		m.popModel()
	}
	return m, nil
}

// startLogViewer 启动日志查看器
func (m *Model) startLogViewer() {
	running, err := asaserver.IsServerRunning(m.managingInstance)
	if err != nil {
		m.showMessage("错误", err.Error(), "error")
		return
	}
	if !running {
		m.showMessage("错误", "实例未在运行中", "error")
		return
	}
	logPath, exists := asaserver.GetInstanceLogFile(m.managingInstance)
	if !exists {
		var err error
		logPath, err = asaserver.GetGameLogFilePath(m.managingInstance)
		if err != nil {
			m.showMessage("错误", fmt.Sprintf("获取日志文件路径失败: %v", err), "error")
			return
		}
	}

	// 初始化日志显示相关字段
	m.logLines = []string{
		fmt.Sprintf("日志文件: %s", logPath),
		"实时监听日志中...",
		"",
	}
	m.logReading = true
	m.logStopChan = make(chan bool, 1)
	m.logLastLen = len(m.logLines)
	m.logInitDone = true

	// 在goroutine中加载日志
	go func() {
		stopChan := asaserver.TailLogFile(logPath, func(line string) {
			select {
			case <-m.logStopChan:
				return
			default:
				m.logLines = append(m.logLines, line)
				if len(m.logLines) > 1000 {
					m.logLines = m.logLines[len(m.logLines)-1000:]
				}
			}
		})

		<-m.logStopChan
		stopChan()
	}()

	s := spinner.New()
	s.Spinner = spinner.Dot
	m.logSpinner = s
	m.logViewport = viewport.New(m.width, m.height-3)
	// 设置初始内容为日志信息
	initContent := strings.Join(m.logLines, "\n")
	m.logViewport.SetContent(initContent)
	// 初始化时直接跳转到底部
	m.logViewport.GotoBottom()
	m.logInitDone = false
	m.pushModel(LogViewerModel)
}

// stopLogViewer 停止日志查看器
func (m *Model) stopLogViewer() {
	if m.logReading {
		m.logReading = false
		m.logLastLen = 0
		m.logInitDone = false
		select {
		case m.logStopChan <- true:
		default:
		}
		close(m.logStopChan)
	}
}

// handleLogViewerKeys 处理日志查看器按键
func (m *Model) handleLogViewerKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.logViewport, cmd = m.logViewport.Update(msg)
	return m, cmd
}

// viewLogViewer 渲染日志查看器
func (m *Model) viewLogViewer() string {
	// 更新viewport内容（仅当有新日志时）
	if len(m.logLines) > m.logLastLen {
		content := strings.Join(m.logLines, "\n")
		m.logViewport.SetContent(content)
		m.logLastLen = len(m.logLines)
		// 有新日志时跳转到下方
		m.logViewport.GotoBottom()
	}

	helpText := "\n↑↓ 滑动  Ctrl+C/Esc 退出"

	return fmt.Sprintf(
		"%s\n%s\n%s",
		"=== 实时日志 ===",
		m.logViewport.View(),
		helpText,
	)
}

// showMessage 显示消息
func (m *Model) showMessage(title, body, msgType string) {
	m.messageTitle = title
	m.messageBody = body
	m.messageType = msgType
	m.pushModel(MessageModel)
}
func (m *Model) pushModel(modelType ModelType) {
	m.modelStack = append(m.modelStack, m.currentModel)
	m.currentModel = modelType
}

// popModel 从栈弹出模型
func (m *Model) popModel() {
	if len(m.modelStack) > 0 {
		m.currentModel = m.modelStack[len(m.modelStack)-1]
		m.modelStack = m.modelStack[:len(m.modelStack)-1]
	}
}

// 视图渲染函数
func (m *Model) viewInstanceSelect() string {
	return m.instanceList.View()
}

func (m *Model) viewInstanceManage() string {
	return m.manageList.View()
}

func (m *Model) viewBackupSelect() string {
	return m.backupList.View()
}

func (m *Model) viewRCONInput() string {
	return fmt.Sprintf(
		"%s%s%s",
		m.rconViewport.View(),
		gap,
		m.rconTextarea.View(),
	)
}

func (m *Model) viewRenameInput() string {
	content := "=== 重命名实例 ===\n\n"
	content += m.inputLabel + "\n\n"
	content += "> " + m.inputValue + "_\n"
	content += "\n输入新名称  Enter 确认  Esc 返回"
	return content
}

func (m *Model) viewMessage() string {
	content := "=== " + m.messageTitle + " ===\n\n"
	content += m.messageBody + "\n"
	content += "\n按 Enter 或空格继续..."
	return content
}

// getManageOptions 获取管理选项列表
func getManageOptions() []string {
	return []string{
		"启动服务器",
		"停止服务器",
		"重启服务器",
		"查看状态",
		"发送RCON命令",
		"备份世界",
		"恢复备份",
		"查看日志",
		"编辑配置",
		"重命名实例",
		"返回",
	}
}

// renameWithFallback 带回退的重命名
func renameWithFallback(oldPath, newPath string) error {
	return nil
}

// NewModel 创建新模型
func NewModel() *Model {
	// 创建实例列表
	instanceList := list.New([]list.Item{}, list.NewDefaultDelegate(), 80, 20)
	instanceList.Title = "选择实例"

	// 创建管理菜单列表
	manageList := list.New([]list.Item{}, itemDelegate{}, 80, 20)
	manageList.Title = "管理菜单"

	// 创建备份列表
	backupList := list.New([]list.Item{}, itemDelegate{}, 80, 20)
	backupList.Title = "选择备份"

	// 创建RCON聊天组件
	ta := textarea.New()
	ta.Placeholder = "输入命令..."
	ta.Prompt = "┃ "
	ta.CharLimit = 280
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)
	vp := viewport.New(80, 15)
	vp.SetContent("欢迎进入RCON终端")

	model := &Model{
		modelStack:   []ModelType{},
		instanceList: instanceList,
		manageList:   manageList,
		backupList:   backupList,
		rconTextarea: ta,
		rconViewport: vp,
		rconMessages: []string{},
		rconSender:   lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		currentModel: InstanceSelectModel,
	}

	// 加载实例列表
	instances, _ := asaserver.GetAvailableInstances()
	model.instances = instances
	model.initInstanceList()

	return model
}
