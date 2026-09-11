package arkapimanage

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"asa-server/internal/plugindata"
	"asa-server/pkg/logger"
)

// 插件备份：被更新或卸载替换下来的插件目录整个挪进实例的 ArkApi/Backups/<插件名>-<时间戳>/，
// 配置与数据随之保留，重装时可以从最近一份恢复。每个插件保留最近 keepBackups 份（方案 §6.0）。

const keepBackups = 3

// backupSuffix 匹配备份目录名里插件名之后的部分。必须严格匹配：插件 Foo 的前缀
// "Foo-" 也是插件 Foo-Bar 的前缀，只看前缀会把别人的备份当成自己的删掉。
var backupSuffix = regexp.MustCompile(`^\d{8}-\d{6}(-\d+)?$`)

// newBackupPath 返回一个尚不存在的备份目录路径（同一秒内多次操作时加序号）。
func newBackupPath(instanceName, plugin string) (string, error) {
	root := plugindata.InstanceBackupsDir(instanceName)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	base := filepath.Join(root, plugin+"-"+time.Now().Format("20060102-150405"))
	p := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p, nil
		}
		p = fmt.Sprintf("%s-%d", base, i)
	}
}

// pluginBackups 返回插件的全部备份目录，旧的在前。
func pluginBackups(instanceName, plugin string) []string {
	root := plugindata.InstanceBackupsDir(instanceName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if rest, ok := strings.CutPrefix(e.Name(), plugin+"-"); ok && backupSuffix.MatchString(rest) {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out) // 时间戳定宽，按名字排就是按时间排
	for i, n := range out {
		out[i] = filepath.Join(root, n)
	}
	return out
}

func latestBackup(instanceName, plugin string) string {
	if b := pluginBackups(instanceName, plugin); len(b) > 0 {
		return b[len(b)-1]
	}
	return ""
}

func pruneBackups(instanceName, plugin string) {
	b := pluginBackups(instanceName, plugin)
	for len(b) > keepBackups {
		if err := os.RemoveAll(b[0]); err != nil {
			logger.Warnf("清理旧的插件备份 %s 失败: %v", b[0], err)
		}
		b = b[1:]
	}
}
