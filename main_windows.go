//go:build windows

package main

import (
	"asa-server/internal/gui"
	"asa-server/internal/svcmgr"
	"context"

	"github.com/urfave/cli/v3"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/eventlog"
)

// reportStartupConfigErrorPlatform 补上 Windows 才有的两个渠道：
//   - 服务模式：stderr 没人看，写一条 Windows 事件日志（应用程序日志）。事件源由
//     kardianos 在 service install 时按服务名注册（eventlog.InstallAsEventCreate），
//     打不开就算了，文件日志那份已经写过（配置文件存在时）。
//   - GUI（双击运行）：没有控制台，直接退出等于闪退，弹一个原生错误框。缺配置时 GUI
//     走首次设置向导，不会到这里，到这里的只有「配置文件无效」。
func reportStartupConfigErrorPlatform(mode startupMode, msg string) {
	if mode.service {
		if el, err := eventlog.Open(svcmgr.ServiceName); err == nil {
			_ = el.Error(1, msg)
			_ = el.Close()
		}
	}
	if mode.gui {
		text, err1 := windows.UTF16PtrFromString(msg)
		caption, err2 := windows.UTF16PtrFromString("ASA Server Manager 无法启动")
		if err1 == nil && err2 == nil {
			_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND)
		}
	}
}

var platformCommands = []*cli.Command{
	{
		Name:   "gui",
		Usage:  "Start GUI mode",
		Action: actionGUI,
	},
}

func Commands() []*cli.Command {
	return append(
		commonCommands,
		platformCommands...,
	)
}

// actionGUI starts the GUI application
func actionGUI(ctx context.Context, cmd *cli.Command) error {
	guiApp := gui.NewGUIApp()
	guiApp.Run()
	return nil
}

// runDefaultAction is what a no-argument invocation does on Windows: launch
// the GUI, exactly as before the Linux no-args path was added.
func runDefaultAction(ctx context.Context) error {
	return actionGUI(ctx, nil)
}
