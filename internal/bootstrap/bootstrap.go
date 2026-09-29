// Package bootstrap 是「应用配置 → 各运行时包」的唯一应用点。
//
// 以前 main.go、setup 与 GUI 首次启动向导各自逐字段拼一份 runner.Config——
// runner.Configure 是整体覆盖，漏一个字段就等于在运行中把它清空
// （docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §11 出过事故）；而且 setup / GUI
// 重新加载配置后只重做了 runner.Configure、漏了 download.Configure，用户改的
// 下载代理要重启才生效。现在三处都走这里。见
// docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.6。
//
// webapi 的包级变量（端口 / TLS 等）不在这里：否则 bootstrap 要 import webapi，
// 依赖它的 actions 就把整个 webapi 拖进了自己的依赖闭包。那几行留在 main.go。
package bootstrap

import (
	"fmt"

	"asa-server/internal/appconfig"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"
	"asa-server/pkg/download"
	"asa-server/pkg/logger"
)

// Apply 把 cfg 应用到下载器与 runner。baseDir 是已经解析好的数据目录
// （appconfig.Load 的返回值），不是 cfg.BaseDir 字段——后者可能为空。
func Apply(cfg *appconfig.Config, baseDir string) {
	download.Configure(download.Config{
		GithubProxy: cfg.Download.GithubProxy,
		HTTPProxy:   cfg.Download.HTTPProxy,
		Timeout:     cfg.Download.Timeout,
		Retries:     cfg.Download.Retries,
	})

	// 整体覆盖：linux.* 必须给齐，新增字段只在这里加一次。
	runner.Configure(runner.Config{
		Runtime:          cfg.Linux.Runtime,
		UmuVersion:       cfg.Linux.UmuVersion,
		ProtonVersion:    cfg.Linux.ProtonVersion,
		PrefixMode:       cfg.Linux.PrefixMode,
		PrefixDir:        cfg.Linux.PrefixDir,
		PythonBin:        cfg.Linux.UmuPythonBin,
		AutoDownload:     cfg.Linux.AutoDownload,
		SteamRTPrefetch:  cfg.Linux.SteamRTPrefetch,
		InstallVCRedist:  cfg.Linux.InstallVCRedist,
		VCRedistURL:      cfg.Linux.VCRedistURL,
		VCRedistSHA256:   cfg.Linux.VCRedistSHA256,
		WineDLLOverrides: cfg.Linux.WineDLLOverrides,
		Display:          cfg.Linux.Display,
		XvfbBin:          cfg.Linux.XvfbBin,
		XvfbScreen:       cfg.Linux.XvfbScreen,
		AllowX11Remount:  cfg.Linux.AllowX11Remount,
		GameID:           cfg.Linux.GameID,
		BaseDir:          baseDir,
		RuntimeUser:      cfg.Linux.UmuRuntimeUser,
		RuntimeUID:       cfg.Linux.UmuRuntimeUID,
		RuntimeGID:       cfg.Linux.UmuRuntimeGID,
		RunAsRoot:        cfg.Linux.UmuRunAsRoot,
		RuntimeDeepProbe: cfg.Linux.UmuRuntimeDeepProbe,
	})
}

// Reload 在已经运行的进程里重新走一遍启动引导：重新加载 config.yaml →
// cfgpkg.BaseDir → 建数据目录 → 日志切到新目录 → Apply。顺序与 main.go 启动时
// 一致，给「运行中改了配置 / 刚选定数据目录」的 setup 与 GUI 向导用，不需要重启。
//
// 以 WithoutAutoGenerate 加载：调用方要么刚写好配置，要么就是在检查「用户改完
// 了没有」，这时候替用户凭空生成一份默认配置只会掩盖问题。
//
// 配置加载或校验失败时返回错误，cfgpkg.BaseDir / 日志 / 运行时配置都保持原样
// （appconfig.Get() 也不变，Load 只在校验通过后才替换）。建数据目录失败时
// cfgpkg 的目录变量已经切到新值（EnsureDirectories 先赋值后建目录），日志与运行时
// 配置仍是旧的——调用方应把这当作致命错误提示用户换目录。
func Reload() (string, *appconfig.Config, error) {
	baseDir, err := appconfig.Load(appconfig.WithoutAutoGenerate())
	if err != nil {
		return "", nil, fmt.Errorf("重新加载 %s 失败: %w", appconfig.ConfigPath(), err)
	}
	// EnsureDirectories 自己会切换 cfgpkg.BaseDir 及各派生目录变量。
	if err := cfgpkg.EnsureDirectories(baseDir); err != nil {
		return "", nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	logger.InitLoggerWithBaseDir(baseDir)

	cfg := appconfig.Get()
	Apply(cfg, baseDir)
	return baseDir, cfg, nil
}
