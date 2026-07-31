package certmgr

import (
	"asa-server/internal/logger"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// storeLocation 是受信任根存储的一个位置。顺序即优先级：
// LocalMachine 对所有用户与所有浏览器（含 Firefox 的 enterprise_roots）都最可靠，
// 但需要管理员权限；拿不到就退到 CurrentUser，普通用户也能装上。
type storeLocation struct {
	name string
	flag uint32
}

var storeLocations = []storeLocation{
	{"LocalMachine", windows.CERT_SYSTEM_STORE_LOCAL_MACHINE},
	{"CurrentUser", windows.CERT_SYSTEM_STORE_CURRENT_USER},
}

// Fingerprint 返回证书 DER 的 SHA-1 指纹（大写十六进制），与 Windows
// 证书管理器里显示的「指纹」一致，也是本包做幂等判断的唯一依据。
func Fingerprint(der []byte) string {
	sum := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// TrustCA 把本地 CA 写入系统受信任根存储。已存在同指纹的证书时直接返回，
// 因此每次启动都可以无脑调用；用户手动删掉 CA 后，下次启动会自动补装。
func TrustCA() error {
	_, der, err := readCertPEM(caCertPath())
	if err != nil {
		return fmt.Errorf("读取本地 CA 失败: %w", err)
	}
	fingerprint := Fingerprint(der)

	if loc, ok := findTrusted(fingerprint); ok {
		logger.GetLogger().Debugf("本地 CA 已在 %s\\Root 中（指纹 %s）", loc, fingerprint)
		return nil
	}

	var errs []error
	for _, loc := range storeLocations {
		if err := addToStore(loc, der); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", loc.name, err))
			continue
		}
		logger.GetLogger().Infof(
			"已将本地 CA 写入 %s\\Root（指纹 %s）。如需移除，执行 `asa-server cert uninstall`",
			loc.name, fingerprint)
		return nil
	}

	return errors.Join(errs...)
}

// UntrustCA 把本地 CA 从受信任根存储移除。卸载程序、移除服务时必须调用——
// 往用户系统里装根证书却不提供干净的移除手段是不负责任的。
func UntrustCA() error {
	_, der, err := readCertPEM(caCertPath())
	if err != nil {
		return fmt.Errorf("读取本地 CA 失败: %w", err)
	}
	return untrustFingerprint(Fingerprint(der))
}

// UntrustCAOnCleanup 供卸载 / 移除服务的流程调用：从未生成过本地 CA 时静默返回，
// 不给「压根没开过 TLS」的用户平添一条警告
func UntrustCAOnCleanup() error {
	if _, err := os.Stat(caCertPath()); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return UntrustCA()
}

// IsCATrusted 报告本地 CA 是否已在受信任根存储中，以及在哪个位置
func IsCATrusted() (bool, string, error) {
	_, der, err := readCertPEM(caCertPath())
	if err != nil {
		return false, "", err
	}
	loc, ok := findTrusted(Fingerprint(der))
	return ok, loc, nil
}

// findTrusted 在两个存储位置里查指纹，返回命中的位置名
func findTrusted(fingerprint string) (string, bool) {
	for _, loc := range storeLocations {
		store, err := openRootStore(loc.flag)
		if err != nil {
			// CurrentUser 一定打得开；LocalMachine 未提权时会失败，属正常情况
			continue
		}
		ctx, err := findInStore(store, fingerprint)
		if err == nil && ctx != nil {
			_ = windows.CertFreeCertificateContext(ctx)
			_ = windows.CertCloseStore(store, 0)
			return loc.name, true
		}
		_ = windows.CertCloseStore(store, 0)
	}
	return "", false
}

// untrustFingerprint 从所有存储位置删除指定指纹的证书。两个位置都找不到不算错误——
// 目标状态（CA 不在受信任存储里）已经达成。
func untrustFingerprint(fingerprint string) error {
	var errs []error
	removed := false

	for _, loc := range storeLocations {
		store, err := openRootStore(loc.flag)
		if err != nil {
			continue
		}
		ctx, err := findInStore(store, fingerprint)
		if err != nil || ctx == nil {
			_ = windows.CertCloseStore(store, 0)
			continue
		}
		// CertDeleteCertificateFromStore 无论成败都会释放 ctx，不能再 Free 一次
		if err := windows.CertDeleteCertificateFromStore(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", loc.name, err))
		} else {
			removed = true
			logger.GetLogger().Infof("已从 %s\\Root 移除本地 CA（指纹 %s）", loc.name, fingerprint)
		}
		_ = windows.CertCloseStore(store, 0)
	}

	if !removed && len(errs) == 0 {
		logger.GetLogger().Debugf("受信任存储中未找到指纹 %s，无需移除", fingerprint)
	}
	return errors.Join(errs...)
}

func openRootStore(locationFlag uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("ROOT")
	if err != nil {
		return 0, err
	}
	store, err := windows.CertOpenStore(
		windows.CERT_STORE_PROV_SYSTEM,
		0,
		0,
		locationFlag|windows.CERT_STORE_OPEN_EXISTING_FLAG,
		uintptr(unsafe.Pointer(name)),
	)
	runtime.KeepAlive(name)
	if err != nil {
		return 0, fmt.Errorf("打开受信任根存储失败: %w", err)
	}
	return store, nil
}

func findInStore(store windows.Handle, fingerprint string) (*windows.CertContext, error) {
	hash, err := hex.DecodeString(fingerprint)
	if err != nil {
		return nil, fmt.Errorf("指纹格式错误: %w", err)
	}
	blob := windows.CryptHashBlob{
		Size: uint32(len(hash)),
		Data: &hash[0],
	}
	ctx, err := windows.CertFindCertificateInStore(
		store,
		windows.X509_ASN_ENCODING,
		0,
		windows.CERT_FIND_SHA1_HASH,
		unsafe.Pointer(&blob),
		nil,
	)
	runtime.KeepAlive(hash)
	return ctx, err
}

func addToStore(loc storeLocation, der []byte) error {
	store, err := openRootStore(loc.flag)
	if err != nil {
		return err
	}
	defer windows.CertCloseStore(store, 0)

	ctx, err := windows.CertCreateCertificateContext(windows.X509_ASN_ENCODING, &der[0], uint32(len(der)))
	if err != nil {
		return fmt.Errorf("构建证书上下文失败: %w", err)
	}
	defer windows.CertFreeCertificateContext(ctx)

	if err := windows.CertAddCertificateContextToStore(
		store, ctx, windows.CERT_STORE_ADD_REPLACE_EXISTING, nil,
	); err != nil {
		return fmt.Errorf("写入存储失败: %w", err)
	}
	return nil
}

// IsElevated 报告当前进程是否以管理员身份运行
func IsElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// hardenKey 是 hardenKeyFile 的可替换入口，测试里换掉它以免为临时文件拉起 icacls
var hardenKey = hardenKeyFile

// hardenKeyFile 收紧私钥文件的 ACL：只留 SYSTEM、Administrators 与当前用户。
// Go 在 Windows 上把 0600 映射成只读属性而非真正的 ACL，所以必须额外做这一步。
// 失败只警告——私钥仍在只有本机能读的目录里，功能不受影响。
func hardenKeyFile(path string) {
	sids := []string{"*S-1-5-18", "*S-1-5-32-544"} // SYSTEM, Administrators（用 SID 以免受系统语言影响）
	if sid, err := currentUserSID(); err == nil {
		sids = append(sids, "*"+sid)
	}

	args := []string{path, "/inheritance:r"}
	for _, sid := range sids {
		args = append(args, "/grant:r", sid+":(F)")
	}

	cmd := exec.Command("icacls", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.GetLogger().Warnf("收紧私钥文件权限失败 %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
}

func currentUserSID() (string, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}
