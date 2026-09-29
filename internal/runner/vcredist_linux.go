//go:build linux

package runner

// ArkApi 前置：Wine prefix 里的微软 VC++ 运行时。
//
// 机制在 asa-server/pkg/vcredist（下载安装包、写 DLL override、跑安装器、写标记），
// 「什么时候装、装进哪个 prefix、写共享前缀前过守卫」在 umuruntime 的 vcrt 插件与
// Host 里。本文件只剩组合根的两件事：把 runner.Config 翻成插件配置，以及**把插件
// 的结构化结果翻成人话**。
//
// 后者是这个包边界的关键：凡是要提到 `asa-server setup`、`linux.install_vcredist`、
// 「ArkApi 实例同样起不来」这些本程序自己的名字的地方，pkg 侧一律返回类型
// （umuruntime.Outcome / vcredist.Result.Skip / *AutoDownloadDisabledError /
// OnUnverifiedDownload 钩子），文案全部在这里拼。见 docs/UMU_RUNTIME_PLUGIN_PLAN.md §6。

import (
	"errors"
	"fmt"
	"path/filepath"

	"asa-server/pkg/umuruntime"
	"asa-server/pkg/umuruntime/plugins/vcrt"
	"asa-server/pkg/vcredist"
	"asa-server/pkg/xvfb"
)

// vcrtPlugin 是本进程唯一的 VC++ 插件，与 displayRes 一同注册进 runtimeHost。
var vcrtPlugin = vcrt.New(vcrt.Config{})

// vcrtFor 用 cfg 刷新 vcrtPlugin 并返回它。
func vcrtFor(cfg Config) *vcrt.Plugin {
	vcrtPlugin.Reconfigure(vcrt.Config{
		// custom 运行时的 prefix 是用户自己搭的，不归我们改。
		Managed:      cfg.Runtime == "umu",
		Install:      cfg.InstallVCRedist,
		Dir:          filepath.Join(cfg.BaseDir, "vcredist"),
		URL:          cfg.VCRedistURL,
		SHA256:       cfg.VCRedistSHA256,
		AutoDownload: cfg.AutoDownload,
		// 下载**之前**说，不是事后 —— 事后说的时候 24 MiB 已经无校验地下完了。
		// 后半句提到本程序的配置项，所以只能在这一侧写。
		OnUnverifiedDownload: func(url string, logf func(string, ...any)) {
			logf("警告：%s 的地址里没有可用的 SHA256（自定义镜像？），本次下载不做校验；"+
				"可用 linux.vcredist_sha256 显式指定", url)
		},
	})
	return vcrtPlugin
}

// describeOutcome 是 Host 的 OnOutcome：把插件在 Ensure / EnsurePrefix 里的结果翻成
// 面向本程序用户的话。目前只有 vcrt 会产生结果。
func describeOutcome(o umuruntime.Outcome, logf func(string, ...any)) {
	if o.Plugin != vcrt.Name {
		if o.Kind == umuruntime.Failed {
			logf("运行时组件 %s 安装失败（%v）", o.Plugin, o.Cause)
		}
		return
	}

	switch o.Kind {
	case umuruntime.Failed:
		// 失败不阻断：VC++ 服务的是一个**可选功能**，不开 ArkApi 的用户占绝大多数，
		// 为它让环境准备或实例启动失败不成比例。但必须响亮 —— 真要用 ArkApi 的人
		// 必须看见这条。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.2。
		if o.Key == "" {
			logf("VC++ 运行时安装失败（%v）；不使用 ArkApi 可忽略，使用 ArkApi 请看上面的输出",
				describeVCRedistError(o.Cause))
		} else {
			logf("实例 %s 的 Wine 前缀里安装 VC++ 运行时失败（%v）；不使用 ArkApi 可忽略",
				o.Key, describeVCRedistError(o.Cause))
		}

	case umuruntime.Degraded:
		// 两种「跳过安装器」都**不是失败**：第一步的 DLL override 已经写好，普通实例
		// 不受影响。但代价要说清楚 —— ArkApi 在这台机器上同样起不来（AsaApiLoader.exe
		// 也要求有图形显示），不是只有 system32 没补齐。
		res, _ := o.Detail.(vcredist.Result)
		switch res.Skip {
		case vcredist.SkipDisplayUnavailable:
			// 有显示能力但这次没拿到（多半是 Xvfb 起不来）。与下面「本机没有显示」
			// 同样只跳过安装、不阻断 setup，但原因不同，要如实说。
			logf("跳过 VC++ 运行时安装：拿不到图形显示。%v", o.Cause)
			logf("  override 已经写好，普通实例不受影响；但 **ArkApi 实例同样起不来**")
		case vcredist.SkipNoDisplay:
			// 缺显示在 preflight 里只是**建议项**（缺它只影响 ArkApi，见 checkDisplay），
			// 所以一台没装 Xvfb 的机器会一路走到这里 —— 这条分支是常规路径，不是意外。
			logf("跳过 VC++ 运行时安装：%v。", o.Cause)
			logf("  override 已经写好，普通实例不受影响；但 **ArkApi 实例同样起不来** ——")
			logf("  AsaApiLoader.exe 也要求有图形显示。请%s，然后重跑 asa-server setup。", xvfb.InstallHint)
		}
	}
}

// describeVCRedistError 把「自动下载关了、本地又没有安装包」翻成带本程序配置项的指引。
func describeVCRedistError(err error) error {
	var noDownload *vcredist.AutoDownloadDisabledError
	if errors.As(err, &noDownload) {
		return fmt.Errorf("auto_download 已关闭且本地没有 %s；"+
			"请手动下载 %s 放到该路径，或设 linux.install_vcredist: false",
			noDownload.Dest, noDownload.URL)
	}
	return err
}
