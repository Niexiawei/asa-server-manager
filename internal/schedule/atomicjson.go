package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// writeJSONAtomic 把 v 序列化成缩进 JSON 并原子地写入 path。
//
// 先写临时文件再 os.Rename 覆盖：调度器会在后台不断改写 NextRunAt、追加执行日志，
// 进程被杀在写一半的概率比手工编辑高得多，直接覆写会留下损坏的文件。
//
// 任一步失败都清掉临时文件，否则 BaseDir 会攒一地 .tmp。
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize %s: %w", filepath.Base(path), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to replace %s: %w", path, err)
	}

	return nil
}
