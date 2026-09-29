// 首次启动向导共用逻辑：Windows Fyne 首次启动向导、`asa-server config init` 与
// `asa-server setup` 都调这里的函数，不各写一份。见 docs/LINUX_COMPATIBILITY_PLAN.md §10.4/§10.5 G3/G4。
package appconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"asa-server/pkg/fsutil"
)

// minFreeBytesForNewInstall 是 §10.4 规则 1 的硬性下限：ARK 本体约 25GB，
// 加上存档增长，向导要求新建安装至少预留 30GB。
const minFreeBytesForNewInstall = 30 * 1024 * 1024 * 1024

// ValidateBaseDir 校验用户在首次启动向导里选的数据目录，§10.4 的两条硬性规则：
//  1. 可写 + 剩余空间 ≥ 30GB；目录下已存在 config.yaml 时视为"接管已有安装"而不是
//     新建，跳过空间要求——那多半是老部署，数据已经在那儿了，不该被新建的空间
//     下限拦住。
//  2. 不能是映射网络盘/网络文件系统：BadgerDB 用 mmap + 文件锁
//     （{BaseDir}/database_file/state_db），在 SMB/CIFS/NFS 上不可靠，可能直接
//     损坏实例状态库。
//
// 校验失败时的错误信息本身就是给最终用户看的提示文案（Fyne 对话框/CLI 直接原样
// 展示），不是内部诊断信息，因此不用 %w 包裹底层错误、措辞刻意写成"换一个目录"
// 这样可执行的建议，而不是笼统的"校验失败"。
func ValidateBaseDir(dir string) error {
	if isNetwork, err := fsutil.IsNetworkDrive(dir); err == nil && isNetwork {
		return fmt.Errorf("不能选择映射网络盘或网络文件系统目录：%s\n"+
			"BadgerDB 依赖本地文件锁，在网络存储上可能损坏实例状态库，请换一个本地磁盘目录", dir)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("目录不可写：%s（%v），请换一个有写权限的本地磁盘目录", dir, err)
	}
	probe := filepath.Join(dir, ".asa-server-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("目录不可写：%s（%v），请换一个有写权限的本地磁盘目录", dir, err)
	}
	_ = os.Remove(probe)

	adoptingExisting := fileExists(filepath.Join(dir, ConfigFileName))
	if !adoptingExisting {
		free, err := fsutil.FreeBytes(dir)
		if err != nil {
			return fmt.Errorf("无法读取磁盘剩余空间：%s（%v）", dir, err)
		}
		if free < minFreeBytesForNewInstall {
			return fmt.Errorf("剩余空间不足：ARK 服务端本体约 25GB，建议至少预留 30GB，"+
				"%s 当前仅剩 %.1f GB，请换一个空间更充足的本地磁盘目录", dir, float64(free)/(1<<30))
		}
	}
	return nil
}
