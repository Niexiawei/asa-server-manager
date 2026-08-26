//go:build linux

package certmgr

import (
	"asa-server/internal/logger"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// caAnchorFileName 是写入系统信任存储的文件名。要求 .crt 后缀——
// update-ca-certificates 只扫描 /usr/local/share/ca-certificates/**/*.crt。
const caAnchorFileName = "asa-server-ca.crt"

// trustBackend 描述一种发行版的信任锚点约定：往哪写、写完用什么命令生效。
// 两者互斥，按可执行文件是否存在探测（Debian 系 vs RHEL 系），见
// docs/LINUX_COMPATIBILITY_PLAN.md §5.7。
type trustBackend struct {
	anchorPath string
	updateCmd  []string
}

// detectBackend 探测本机可用的信任存储更新工具。找不到时返回的错误
// 会被 TrustCA/UntrustCA 的调用方按「装不上，只降级为浏览器警告」处理。
func detectBackend() (*trustBackend, error) {
	if _, err := exec.LookPath("update-ca-certificates"); err == nil {
		return &trustBackend{
			anchorPath: filepath.Join("/usr/local/share/ca-certificates", caAnchorFileName),
			updateCmd:  []string{"update-ca-certificates"},
		}, nil
	}
	if _, err := exec.LookPath("update-ca-trust"); err == nil {
		return &trustBackend{
			anchorPath: filepath.Join("/etc/pki/ca-trust/source/anchors", caAnchorFileName),
			updateCmd:  []string{"update-ca-trust", "extract"},
		}, nil
	}
	return nil, errors.New("未找到系统信任存储更新工具（update-ca-certificates 或 update-ca-trust），无法安装本地 CA")
}

func runUpdateCmd(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s 执行失败: %w (%s)", argv[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installedFingerprint 读取 anchorPath 处已安装证书的指纹。文件不存在、
// 不是合法 PEM 证书都视为「未安装」而非错误——调用方只关心装没装。
func installedFingerprint(anchorPath string) (string, bool) {
	_, der, err := readCertPEM(anchorPath)
	if err != nil {
		return "", false
	}
	return Fingerprint(der), true
}

// TrustCA 把本地 CA 写入系统信任存储（ca-certificates 或 ca-trust anchors）。
// 已装且指纹匹配时直接返回，因此每次启动都可以无脑调用。
func TrustCA() error {
	if !IsElevated() {
		return errors.New("安装到系统信任存储需要 root 权限，请用 sudo 重新运行该命令")
	}

	_, der, err := readCertPEM(caCertPath())
	if err != nil {
		return fmt.Errorf("读取本地 CA 失败: %w", err)
	}
	fingerprint := Fingerprint(der)

	backend, err := detectBackend()
	if err != nil {
		return err
	}

	if fp, ok := installedFingerprint(backend.anchorPath); ok && fp == fingerprint {
		logger.GetLogger().Debugf("本地 CA 已在 %s 中（指纹 %s）", backend.anchorPath, fingerprint)
		return nil
	}

	pemBytes, err := os.ReadFile(caCertPath())
	if err != nil {
		return fmt.Errorf("读取本地 CA 失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(backend.anchorPath), 0755); err != nil {
		return fmt.Errorf("创建信任锚点目录失败: %w", err)
	}
	if err := os.WriteFile(backend.anchorPath, pemBytes, 0644); err != nil {
		return fmt.Errorf("写入信任锚点 %s 失败: %w", backend.anchorPath, err)
	}
	if err := runUpdateCmd(backend.updateCmd); err != nil {
		return err
	}

	logger.GetLogger().Infof(
		"已将本地 CA 写入 %s（指纹 %s）。系统信任库不影响浏览器的 NSS 证书库，"+
			"Firefox/Chrome 仍需手动导入 %s。如需移除，执行 `asa-server cert uninstall`",
		backend.anchorPath, fingerprint, caCertPath())
	return nil
}

// UntrustCA 把本地 CA 从系统信任存储移除。
func UntrustCA() error {
	if !IsElevated() {
		return errors.New("从系统信任存储移除本地 CA 需要 root 权限，请用 sudo 重新运行该命令")
	}
	_, der, err := readCertPEM(caCertPath())
	if err != nil {
		return fmt.Errorf("读取本地 CA 失败: %w", err)
	}
	return untrustFingerprint(Fingerprint(der))
}

// UntrustCAOnCleanup 供卸载 / 移除服务的流程调用：从未生成过本地 CA 时静默返回，
// 不给「压根没开过 TLS」的用户平添一条警告。
func UntrustCAOnCleanup() error {
	if _, err := os.Stat(caCertPath()); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return UntrustCA()
}

// IsCATrusted 报告本地 CA 是否已在系统信任存储中，以及具体的锚点文件路径。
func IsCATrusted() (bool, string, error) {
	_, der, err := readCertPEM(caCertPath())
	if err != nil {
		return false, "", err
	}
	loc, ok := findTrusted(Fingerprint(der))
	return ok, loc, nil
}

// findTrusted 查找信任锚点文件是否存在且指纹匹配，返回锚点路径。
func findTrusted(fingerprint string) (string, bool) {
	backend, err := detectBackend()
	if err != nil {
		return "", false
	}
	fp, ok := installedFingerprint(backend.anchorPath)
	if !ok || fp != fingerprint {
		return "", false
	}
	return backend.anchorPath, true
}

// untrustFingerprint 删除指定指纹的信任锚点。找不到发行版更新工具、
// 锚点不存在、或已安装的指纹对不上，都视为「目标状态已达成」而非错误——
// ensureCA 重签时会无条件调用它清理旧 CA，不应该在「压根没装过」时报警告。
func untrustFingerprint(fingerprint string) error {
	backend, err := detectBackend()
	if err != nil {
		return nil
	}
	fp, ok := installedFingerprint(backend.anchorPath)
	if !ok || fp != fingerprint {
		return nil
	}
	if err := os.Remove(backend.anchorPath); err != nil {
		return fmt.Errorf("删除信任锚点 %s 失败: %w", backend.anchorPath, err)
	}
	if err := runUpdateCmd(backend.updateCmd); err != nil {
		return err
	}
	logger.GetLogger().Infof("已从 %s 移除本地 CA（指纹 %s）", backend.anchorPath, fingerprint)
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
