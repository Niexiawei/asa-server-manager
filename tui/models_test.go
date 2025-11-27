package tui

import (
	"testing"
)

// TestNewModel 测试创建新模型
func TestNewModel(t *testing.T) {
	model := NewModel()
	if model == nil {
		t.Error("NewModel should not return nil")
	}

	if model.currentModel != InstanceSelectModel {
		t.Errorf("Initial model type should be InstanceSelectModel, got %v", model.currentModel)
	}

	if len(model.modelStack) != 0 {
		t.Errorf("Initial model stack should be empty, got length %d", len(model.modelStack))
	}
}

// TestModelPushPop 测试模型栈的推入和弹出
func TestModelPushPop(t *testing.T) {
	model := NewModel()

	// 测试推入
	model.pushModel(InstanceManageModel)
	if model.currentModel != InstanceManageModel {
		t.Errorf("After push, currentModel should be InstanceManageModel, got %v", model.currentModel)
	}

	if len(model.modelStack) != 1 {
		t.Errorf("After push, stack length should be 1, got %d", len(model.modelStack))
	}

	if model.modelStack[0] != InstanceSelectModel {
		t.Errorf("After push, stack[0] should be InstanceSelectModel, got %v", model.modelStack[0])
	}

	// 测试弹出
	model.popModel()
	if model.currentModel != InstanceSelectModel {
		t.Errorf("After pop, currentModel should be InstanceSelectModel, got %v", model.currentModel)
	}

	if len(model.modelStack) != 0 {
		t.Errorf("After pop, stack length should be 0, got %d", len(model.modelStack))
	}
}

// TestShowMessage 测试消息显示
func TestShowMessage(t *testing.T) {
	model := NewModel()

	model.showMessage("测试标题", "测试内容", "info")

	if model.currentModel != MessageModel {
		t.Errorf("After showMessage, currentModel should be MessageModel, got %v", model.currentModel)
	}

	if model.messageTitle != "测试标题" {
		t.Errorf("Message title mismatch, expected '测试标题', got '%s'", model.messageTitle)
	}

	if model.messageBody != "测试内容" {
		t.Errorf("Message body mismatch, expected '测试内容', got '%s'", model.messageBody)
	}

	if model.messageType != "info" {
		t.Errorf("Message type mismatch, expected 'info', got '%s'", model.messageType)
	}
}

// TestInit 测试初始化
func TestInit(t *testing.T) {
	model := NewModel()
	cmd := model.Init()
	if cmd != nil {
		t.Error("Init should return nil command")
	}
}

// TestView 测试视图渲染
func TestView(t *testing.T) {
	model := NewModel()

	// 测试主菜单视图
	view := model.View()
	if view == "" {
		t.Error("Main menu view should not be empty")
	}

	if len(view) == 0 {
		t.Error("View output should not be empty")
	}
}

// TestGetManageOptions 测试获取管理选项
func TestGetManageOptions(t *testing.T) {
	options := getManageOptions()

	if len(options) == 0 {
		t.Error("getManageOptions should return non-empty list")
	}

	expectedOptions := []string{
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

	if len(options) != len(expectedOptions) {
		t.Errorf("Expected %d options, got %d", len(expectedOptions), len(options))
	}

	for i, expected := range expectedOptions {
		if i < len(options) && options[i] != expected {
			t.Errorf("Option %d mismatch, expected '%s', got '%s'", i, expected, options[i])
		}
	}
}
