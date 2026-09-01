package installer

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"
	"asa-server/pkg/console"
	"asa-server/pkg/logger"
	"asa-server/pkg/netutil"
	"asa-server/pkg/procx"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// asaApiLoaderRelPath 是 ArkApi 的加载器在 server-files 里的相对位置。
// 大小写与 mirror/plugindata 里的常量保持一致（那边踩过大小写的坑，见
// internal/plugindata/casecheck_linux.go）。
const asaApiLoaderRelPath = "ShooterGame/Binaries/Win64/AsaApiLoader.exe"

// AsaApiLoaderPath 是 server-files 里 AsaApiLoader.exe 的绝对路径。
func AsaApiLoaderPath() string {
	return filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(asaApiLoaderRelPath))
}

// ArkApiInstalled 报告 server-files 里有没有装 ArkApi。
func ArkApiInstalled() bool {
	fi, err := os.Stat(AsaApiLoaderPath())
	return err == nil && !fi.IsDir()
}

// arkApiVerifyLaunchLogPath 与 verify 的那份分开：两条命令诊断的是不同的东西，
// 互相覆盖会让「刚才那次到底是谁的输出」变成一个需要猜的问题。
func arkApiVerifyLaunchLogPath() string {
	return filepath.Join(cfgpkg.BaseDir, "logs", "verify-arkapi-launch.log")
}

// VerifyArkApiInstallation 真的用 AsaApiLoader.exe 拉起一次服务端，验证 ArkApi 这条
// 启动链路在本机能不能走通。
//
// 与 VerifyServerInstallation 的区别，全部是有意的：
//   - 走 AsaApiLoader.exe 而不是 ArkAscendedServer.exe —— 被验证的就是那层注入；
//   - 带 PTY，与实例真实启动时 `Options.PTY = arkAsaApiRunning` 完全一致
//     （AsaApiLoader 用光标定位排版，不给它终端就不是在验证同一条路径）；
//   - 不看配置目录存不存在 —— 这条命令永远是「重新验一次」，没有可跳过的语义。
//
// 成功判据同样是**端口真的被监听**，不是配置目录出现：后者在重跑时本来就在，
// 等于什么都没验（docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §8）。
func VerifyArkApiInstallation(ctx context.Context, outputCallback ...io.Writer) error {
	var outputWriter io.Writer
	if len(outputCallback) > 0 && outputCallback[0] != nil {
		outputWriter = outputCallback[0]
	}
	emit := func(msg string) {
		logger.Info(msg)
		if outputWriter != nil {
			_, _ = outputWriter.Write([]byte(msg + "\n"))
		}
	}

	// 与 verify/update 共用同一把 server-files 锁：这次启动同样直接跑在
	// server-files 上（没有实例镜像挡在前面），有实例在跑时必须拒绝。
	if err := beginServerFilesUpdate(); err != nil {
		return err
	}
	defer endServerFilesUpdate()

	asaApiExe := AsaApiLoaderPath()
	if !ArkApiInstalled() {
		return fmt.Errorf("没有找到 %s —— 本机没有安装 ArkApi。\n"+
			"ArkApi 需要用户自行下载安装到 server-files 的 ShooterGame/Binaries/Win64/ 下，"+
			"本程序不代为下载", asaApiExe)
	}

	if err := ApplyLinuxFixups(); err != nil {
		logger.Warnf("Failed to apply Linux compatibility fixups: %v", err)
	}

	// 与 VerifyServerInstallation 同一个理由：这次启动没有实例镜像挡在前面，
	// 降权后的游戏进程要直接写 server-files，否则 Saved 都建不出来，
	// 下面的等待必然超时（docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.6）。
	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved"), 0o755); err != nil {
		return fmt.Errorf("failed to create Saved directory: %w", err)
	}
	if err := runner.PrepareSharedTree(cfgpkg.ServerFilesDir); err != nil {
		logger.Warnf("Failed to prepare %s for the runtime user: %v", cfgpkg.ServerFilesDir, err)
	}

	port, err := netutil.FreeUDPPort()
	if err != nil {
		logger.Warnf("Failed to get a free UDP port, falling back to 7777: %v", err)
		port = 7777
	}

	logPath := arkApiVerifyLaunchLogPath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logFile, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if logErr != nil {
		logger.Warnf("Failed to open ArkApi verification launch log: %v", logErr)
	} else {
		defer logFile.Close()
		emit("启动输出: " + logPath)
	}

	emit(fmt.Sprintf("正在用 AsaApiLoader.exe 在端口 %d 上拉起服务端，"+
		"等待它真正开始监听（首次可能要几分钟）...", port))

	handle, err := runner.Run(ctx, asaApiExe, []string{
		"TheIsland_WP?listen",
		fmt.Sprintf("-Port=%d", port),
		"-NoBattlEye",
		"-crossplay",
		"-server",
		"-log",
		"-nosteamclient",
		"-game",
	}, runner.Options{
		// Dir: 实例启动传的是镜像目录（instance/server.go 的 exeWorkDir），这里的
		// 对应物是 exe 自己所在的 Win64 —— 不传就沿用 asa-server 进程的 cwd，
		// 那就不是在验证同一条路径了。
		Dir: filepath.Dir(asaApiExe),
		PTY: true,
		// AsaApiLoader.exe 要图形显示，没有就静默退出码 3（见
		// docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9）。与实例启动同一个开关，
		// 否则这条命令会"验证通过"一条实例走不通的路。
		NeedsDisplay: true,
	})
	if err != nil {
		return fmt.Errorf("启动 AsaApiLoader.exe 失败: %w", err)
	}
	logger.Infof("AsaApiLoader started (launcher PID: %d)", handle.LauncherPID)

	// PTY 模式下 Options.Log 不生效，PTY 就是那条流 —— 与 instance/server.go 一样，
	// 用 CleanScreenOutput 清洗光标定位序列后落盘。
	if logFile != nil && handle.PTY != nil {
		go func() { _ = console.CleanScreenOutput(handle.PTY, logFile) }()
	}

	configDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	waitErr := waitForVerificationServer(ctx, configDir, port, handle.LauncherPID, verifyStartupTimeout, emit)

	logger.Info("Stopping ArkApi verification server...")
	_ = procx.KillTree(handle.LauncherPID)
	if handle.PTY != nil {
		_ = handle.PTY.Close()
	}
	time.Sleep(2 * time.Second)

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		// logPath，不是 verifyLaunchLogPath()：要报的是**这次**写下的那个文件。
		// 另外把 ArkApi 自己的日志也一并 tail 出来 —— 被验证的就是加载器，
		// 它的日志不走控制台、只写 Win64/logs/ArkApi_*.log（每次启动换名），
		// 而那正是「加载器起没起来」的第一手证据。见 internal/instance/arkapilog.go。
		arkApiLogDir := filepath.Join(filepath.Dir(asaApiExe), "logs")
		reportVerificationFailure(logPath,
			filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Logs"), emit, arkApiLogDir)
		emit("提示：ArkApi 自己的日志在 " + arkApiLogDir + "，启动输出在 " + logPath)
		return fmt.Errorf("ArkApi 启动验证失败: %w", waitErr)
	}

	emit("ArkApi 启动验证通过：AsaApiLoader.exe 成功拉起了服务端并开始监听。")
	return nil
}
