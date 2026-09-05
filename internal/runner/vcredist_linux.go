//go:build linux

package runner

// ArkApi 前置：Wine prefix 里的微软 VC++ 运行时。
//
// 编排本身（下载安装包、写 DLL override、跑安装器、写标记）在
// asa-server/pkg/vcredist；本文件是组合根胶水 + **把结构化结果翻成人话**的那一层。
//
// 后者是这个包边界的关键：凡是要提到 `asa-server setup`、`linux.install_vcredist`、
// 「ArkApi 实例同样起不来」这些本程序自己的名字的地方，pkg 侧一律返回类型
// （Result.Skip / *AutoDownloadDisabledError / OnUnverifiedDownload 钩子），
// 文案全部在这里拼。见 docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md §6。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"asa-server/pkg/vcredist"
	"asa-server/pkg/xvfb"
)

func vcRedistDir(cfg Config) string { return filepath.Join(cfg.BaseDir, "vcredist") }

// vcRedistInstallerFor 用当下的 Config 现建一个 Installer。
//
// 不做包级单例：Installer 不持有任何跨调用状态（同 sysUserFor / pkg/sysuser.Manager，
// 与必须 Reconfigure 的 umuRuntime / xvfbMgr 相反）。
func vcRedistInstallerFor(cfg Config, logf func(string, ...any)) *vcredist.Installer {
	return vcredist.New(vcredist.Config{
		Dir:          vcRedistDir(cfg),
		URL:          cfg.VCRedistURL,
		SHA256:       cfg.VCRedistSHA256,
		AutoDownload: cfg.AutoDownload,
		Umu:          umuRuntimeFor(cfg),
		ChownPath:    chownPathForRuntime,

		// 与 ArkApi 启动路径共用同一个显示解析：两者需要显示的原因是同一个
		// （Wine 的 winex11.drv），见 display_linux.go。blocked 与 err 在这里归一
		// 成一个 error：pkg 只需要分「压根没有显示能力」与「有能力但这次没拿到」，
		// 而**哪种算哪种**是本程序的判断（checkDisplay 把缺显示定为建议项，所以
		// 一台没装 Xvfb 的机器走到 blocked 是常规路径，不是意外）。
		AcquireDisplay: func() ([]string, string, error) {
			disp, blocked, err := acquireDisplay()
			switch {
			case blocked != "":
				return nil, "", fmt.Errorf("%w: %s", vcredist.ErrNoDisplay, blocked)
			case err != nil:
				return nil, "", err
			}
			return disp.Env, disp.How, nil
		},

		// 下载**之前**说，不是事后 —— 事后说的时候 24 MiB 已经无校验地下完了。
		// 后半句提到本程序的配置项，所以只能在这一侧写。
		OnUnverifiedDownload: func(url string) {
			logf("警告：%s 的地址里没有可用的 SHA256（自定义镜像？），本次下载不做校验；"+
				"可用 linux.vcredist_sha256 显式指定", url)
		},
	})
}

// ensurePrefixVCRedist 是 runner.EnsurePrefixVCRedist 的实现。
func ensurePrefixVCRedist(ctx context.Context, prefixKey string, progress io.Writer) error {
	return ensureVCRedist(ctx, getConfig(), prefixKey, progressLogger(progress))
}

// ensureVCRedist 把微软 VC++ 运行时装进指定 prefix，并把 pkg 侧的结构化结果翻成
// 面向本程序用户的指引。
//
// 两个 Skip 分支都**不是失败**：第一步的 DLL override 已经写好，普通实例不受影响。
// 但代价要说清楚 —— ArkApi 在这台机器上同样起不来（AsaApiLoader.exe 也要求有图形
// 显示），不是只有 system32 没补齐。
func ensureVCRedist(ctx context.Context, cfg Config, prefixKey string, logf func(string, ...any)) error {
	if !cfg.InstallVCRedist {
		return nil
	}
	// custom 运行时的 prefix 是用户自己搭的，不归我们改。
	if cfg.Runtime != "umu" {
		return nil
	}

	// 全程用同一份 cfg（不是每处各取一次 getConfig()）：中途 Configure 换了指针会
	// 导致「装到 A 前缀、校验 B 前缀」。
	res, err := vcRedistInstallerFor(cfg, logf).Ensure(ctx, prefixDir(cfg, prefixKey), logf)

	var noDownload *vcredist.AutoDownloadDisabledError
	if errors.As(err, &noDownload) {
		return fmt.Errorf("auto_download 已关闭且本地没有 %s；"+
			"请手动下载 %s 放到该路径，或设 linux.install_vcredist: false",
			noDownload.Dest, noDownload.URL)
	}
	if err != nil {
		return err
	}

	switch res.Skip {
	case vcredist.SkipDisplayUnavailable:
		// 有显示能力但这次没拿到（多半是 Xvfb 起不来）。与下面「本机没有显示」
		// 同样只跳过安装、不阻断 setup，但原因不同，要如实说。
		logf("跳过 VC++ 运行时安装：拿不到图形显示。%v", res.SkipCause)
		logf("  override 已经写好，普通实例不受影响；但 **ArkApi 实例同样起不来**")
	case vcredist.SkipNoDisplay:
		// 缺显示在 preflight 里只是**建议项**（缺它只影响 ArkApi，见 checkDisplay），
		// 所以一台没装 Xvfb 的机器会一路走到这里 —— 这条分支是常规路径，不是意外。
		logf("跳过 VC++ 运行时安装：%v。", res.SkipCause)
		logf("  override 已经写好，普通实例不受影响；但 **ArkApi 实例同样起不来** ——")
		logf("  AsaApiLoader.exe 也要求有图形显示。请%s，然后重跑 asa-server setup。", xvfb.InstallHint)
	}
	return nil
}

// prefixHasVCRedist 只读判断某个 prefix 里有没有微软原生 VC++ 运行时。
// 不联网、不改动，可以放心在实例启动这种热路径上调。判据见 vcredist.InstalledIn。
func prefixHasVCRedist(prefixKey string) bool {
	return vcredist.InstalledIn(prefixDir(getConfig(), prefixKey))
}

// --- 诊断 ---------------------------------------------------------------------

// vcRedistStatus 汇总 prefix 的 VC++ 运行时现状，供 `asa-server verify-arkapi` 展示。
// 只读，不联网。gameDir 传游戏 exe 所在目录（可为空则跳过那一列）。
//
// 只读的那一半整个在 vcredist.Inspect 里；这里补的两样都是本包才知道的：运行时
// 选型，以及显示候选链 —— 且**只问计划不动手**，`verify-arkapi --check-only`
// 不该顺手起个 X 服务。报候选链的头一档：安装真跑起来时先试的就是它。
func vcRedistStatus(prefixKey, gameDir string) VCRedistInfo {
	cfg := getConfig()
	info := vcredist.Inspect(prefixDir(cfg, prefixKey), gameDir)
	info.Managed = cfg.Runtime == "umu"

	if plans, blocked := planDisplay(); blocked != "" {
		info.InstallerBlocked = blocked
	} else {
		info.InstallerDisplay = plans[0].How
	}
	return info
}
