package appconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrConfigExists 表示 InitConfig 的目标位置已经有一份 config.yaml。
// 返回的 error 用 %w 包着它并带上路径，调用方用 errors.Is 判断。
var ErrConfigExists = errors.New("配置文件已存在")

// InitOptions 是 InitConfig 的参数，零值即「按默认位置、默认语言生成一份
// basedir 留空的模板」。
type InitOptions struct {
	// Dir 是写入 config.yaml 的目录；空 = DefaultInitDir()。
	Dir string
	// BaseDir 写进 basedir 字段；空 = 留空（数据目录 = 配置文件所在目录）。
	// 相对路径会先转成绝对路径——相对路径的 basedir 在运行时按当前工作目录
	// 解析，服务模式下工作目录与交互式运行时不同，等于埋了个雷。
	BaseDir string
	// Lang 是模板注释语言 zh / en；空 = DefaultTemplateLang()。
	Lang string
	// Force 为 true 时目标已存在也覆盖，覆盖前先把原文件复制为
	// config.yaml.bak-<时间戳>。
	Force bool
}

// DefaultInitDir 是 InitConfig 未指定 Dir 时的目标目录：ASA_CFG（非空时）>
// 可执行文件同级。与 Load 的查找顺序对齐，保证「init 写到哪」就是「下次 Load
// 会读哪」。第三级（系统固定目录）不作为默认值：写那里通常要管理员权限，而且
// 会被 exe 同级的文件遮蔽，要写得由调用方显式指定。
func DefaultInitDir() (string, error) {
	if cfgEnv := os.Getenv("ASA_CFG"); cfgEnv != "" {
		return cfgEnv, nil
	}
	dir, err := executableDir()
	if err != nil {
		return "", fmt.Errorf("解析可执行文件目录失败: %w", err)
	}
	return dir, nil
}

// InitConfig 渲染模板并写入 config.yaml，返回写入的绝对路径。
//
// 只写这一个文件（必要时建它所在的目录），不建任何数据子目录、不校验 BaseDir——
// 校验（可写、非网络盘、剩余空间）是调用方的事，见 ValidateBaseDir。
//
// 目标已存在且 !Force 时返回包着 ErrConfigExists 的错误，不动原文件。写入是
// 原子的（同目录临时文件 + rename），中途失败不会留下半截配置。
func InitConfig(o InitOptions) (string, error) {
	lang, err := NormalizeLang(o.Lang)
	if err != nil {
		return "", err
	}

	dir := o.Dir
	if dir == "" {
		if dir, err = DefaultInitDir(); err != nil {
			return "", err
		}
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return "", fmt.Errorf("解析配置目录失败: %w", err)
	}

	baseDir := o.BaseDir
	if baseDir != "" {
		if baseDir, err = filepath.Abs(baseDir); err != nil {
			return "", fmt.Errorf("解析数据目录失败: %w", err)
		}
	}

	path := filepath.Join(dir, ConfigFileName)
	if info, statErr := os.Stat(path); statErr == nil {
		if info.IsDir() {
			return "", fmt.Errorf("%s 是一个目录，无法写入配置文件", path)
		}
		if !o.Force {
			return "", fmt.Errorf("%w：%s", ErrConfigExists, path)
		}
		if err := backupFile(path, info.Mode().Perm()); err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}
	if err := writeFileAtomic(path, renderTemplate(lang, baseDir), 0o644); err != nil {
		return "", fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return path, nil
}

// backupFile 把 path 复制为 path.bak-<时间戳>。用复制而不是改名：改名之后如果
// 新文件写失败，原配置就只剩一个备份名，下次启动等于配置丢了。
func backupFile(path string, perm os.FileMode) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取待备份的 %s 失败: %w", path, err)
	}
	bak := path + ".bak-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(bak, raw, perm); err != nil {
		return fmt.Errorf("备份 %s 失败: %w", path, err)
	}
	return nil
}

// writeFileAtomic 先写同目录临时文件再 rename 覆盖目标。同目录保证 rename 不跨
// 文件系统；Windows 上 os.Rename 走 MoveFileEx(MOVEFILE_REPLACE_EXISTING)，
// 目标已存在也能替换。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后这里是无害的 ENOENT

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
