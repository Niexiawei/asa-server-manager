package config

import (
	"fmt"
	"strings"

	"asa-server/pkg/fsutil"
)

// ValidateInstanceName 是实例名的**安全**校验：任何拿实例名去拼路径的地方都先过它。
// 挡住的是会让路径跑出 instances/<名字> 的写法 —— 路径穿越、分隔符、NUL，以及「.」
// （filepath.Join(InstancesDir, ".") 就是 instances/ 本身）。
//
// 它刻意不做可移植性检查：已经存在的实例可能用了 Linux 允许、Windows 不允许的名字，
// 在这里拒绝它们等于让用户再也管理不了自己的实例。可移植性只在**起名**时拦，
// 见 ValidateNewInstanceName。
func ValidateInstanceName(name string) error {
	if name == "" {
		return fmt.Errorf("instance name is required")
	}
	if name == "." || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("invalid instance name")
	}
	return nil
}

// ValidateNewInstanceName 校验一个**新**实例名（创建、重命名）：安全校验之外，还要求
// 名字在 Windows 与 Linux 上都能原样建成目录（fsutil.ValidPortableName）—— 数据目录
// 可能在两个平台之间搬。
func ValidateNewInstanceName(name string) error {
	if err := ValidateInstanceName(name); err != nil {
		return err
	}
	if err := fsutil.ValidPortableName(name); err != nil {
		return fmt.Errorf("实例名不可用: %w", err)
	}
	return nil
}
