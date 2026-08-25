//go:build linux

package certmgr

import (
	"errors"
	"os"

	"asa-server/internal/logger"
)

// errNotImplemented 标记「编译期存根」。真正的 Linux 信任存储实现
// （ca-certificates / ca-trust anchors，见 docs/LINUX_COMPATIBILITY_PLAN.md §5.7）
// 要到 P5 才落地，这里先保证依赖方能编译。
var errNotImplemented = errors.New("certmgr: not implemented on linux yet")

// TrustCA 把本地 CA 写入系统信任存储。
func TrustCA() error {
	return errNotImplemented
}

// UntrustCA 把本地 CA 从系统信任存储移除。
func UntrustCA() error {
	return errNotImplemented
}

// UntrustCAOnCleanup 供卸载 / 移除服务的流程调用。调用方把错误当警告处理，
// 不会阻断卸载本身。
func UntrustCAOnCleanup() error {
	return errNotImplemented
}

// IsCATrusted 报告本地 CA 是否已在系统信任存储中。
func IsCATrusted() (bool, string, error) {
	return false, "", errNotImplemented
}

// findTrusted 在信任存储里查指纹。存根始终报告未命中。
func findTrusted(fingerprint string) (string, bool) {
	return "", false
}

// untrustFingerprint 从信任存储删除指定指纹的证书。存根始终成功——
// 目标状态（未装的证书当然不在存储里）在「什么都没做」时也成立。
func untrustFingerprint(fingerprint string) error {
	return nil
}

// IsElevated 报告当前进程是否以 root 身份运行。装系统信任锚点需要 root
// （ca-trust update 命令会写 /usr/local/share/ca-certificates 之类的系统目录）。
func IsElevated() bool {
	return os.Geteuid() == 0
}

// hardenKey 是 hardenKeyFile 的可替换入口，测试里换掉它以免为临时文件拉起
// 平台特定的权限收紧命令。
var hardenKey = hardenKeyFile

// hardenKeyFile 收紧私钥文件的权限。Windows 侧需要额外拉起 icacls（Go 在
// Windows 上把 0600 映射成只读属性而非真正的 ACL）；Linux 上 os.Chmod 本身
// 就是权限的真相，不需要额外一层。
func hardenKeyFile(path string) {
	if err := os.Chmod(path, 0600); err != nil {
		logger.GetLogger().Warnf("收紧私钥文件权限失败 %s: %v", path, err)
	}
}
