//go:build linux

package runner

// 自管 Xvfb 的机制本身（拉起/看门狗/认领/socket 目录 remount）在 asa-server/pkg/xvfb。
// 本文件只是把它接到 runner.Config 上的胶水：解析出运行时用户的 home/uid/gid/
// 凭证该怎么问（都转给 sysuser 那一层），以及状态文件该落在 BaseDir 下的哪。

import (
	"path/filepath"
	"syscall"

	"asa-server/pkg/xvfb"
)

// xvfbMgr 是本进程唯一一个 Xvfb 管理器——"进程内只有一个自管显示"这条不变量，
// 落地为"只持有一份 *xvfb.Manager"，而不是 pkg/xvfb 自己维护包级单例。
var xvfbMgr = xvfb.New(xvfb.Config{})

// xvfbManager 用当下的 runner.Config 刷新 xvfbMgr 并返回它。
//
// 每次使用前都刷新，而不是只在 Configure() 时刷新一次：Reconfigure 只是一次原子
// 指针替换，开销可以忽略，这样就不需要在跨平台的 Configure() 里为 Linux 单开一个
// 钩子，也不会有"忘了在某个 Configure 调用点里同步"的问题。
func xvfbManager() *xvfb.Manager {
	xvfbMgr.Reconfigure(xvfb.Config{
		Bin:             getConfig().XvfbBin,
		Screen:          getConfig().XvfbScreen,
		StatePath:       xvfbStatePath(getConfig()),
		AllowX11Remount: getConfig().AllowX11Remount,
		HomeDir:         func() string { return runtimeHomeDir(getConfig()) },
		ChildIDs:        func() (uint32, uint32, bool) { return runtimeChildIDs(getConfig()) },
		Credential: func() (*syscall.Credential, error) {
			cred, _, err := resolveRuntimeCredential(getConfig())
			return cred, err
		},
	})
	return xvfbMgr
}

func xvfbStatePath(cfg Config) string {
	if cfg.BaseDir == "" {
		return ""
	}
	return filepath.Join(cfg.BaseDir, "xvfb.state")
}

// stopManagedXvfb 是 stopManagedDisplay（display_linux.go）的实现。
func stopManagedXvfb() { xvfbManager().Stop() }
