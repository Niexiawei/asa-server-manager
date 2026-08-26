package plugindata

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"asa-server/pkg/logger"

	// 纯 Go 的 SQLite 驱动，鉴权库已经在用，这里复用不增加依赖。
	_ "modernc.org/sqlite"
)

const (
	// DefaultSnapshotInterval 是在线快照的默认周期。
	// 保守取值：快照的代价是把整个库读一遍，而它换来的是把崩溃时的最坏损失
	// 从「整个会话」收窄到「一个周期」。
	DefaultSnapshotInterval = 5 * time.Minute

	// minSnapshotInterval 防止用户把周期配成 0 或极小值，让快照反过来拖垮 I/O。
	minSnapshotInterval = 30 * time.Second

	// maxSnapshotDBBytes 超过这个大小的库跳过快照。
	// VACUUM INTO 会完整读一遍源库，几百 MB 的库每 5 分钟读一次会明显与游戏进程争 I/O，
	// 而这类库真正该走的是 docs/ARKAPI_PLUGIN_DATA_PLAN.md §6 的整目录 junction。
	maxSnapshotDBBytes = 512 << 20
)

// runners 记录每个实例的快照 goroutine 取消函数。
var (
	runnersMu sync.Mutex
	runners   = map[string]context.CancelFunc{}
)

// StartSnapshots 为一个已启动的实例开启插件数据库的定时在线快照。
//
// 快照解决的是「回收没能执行」的场景：ARK 崩溃、断电、管理器被杀。
// 那些情况下 Rescue 仍会优先抢救镜像里真实的文件组 —— 快照是**兜底，不是首选**
// （见 snapshotOnce 的说明）。
//
// interval <= 0 表示用默认周期；调用方传负值可用于关闭。重复调用会先停掉旧的。
func StartSnapshots(instanceName, mirrorDir string, interval time.Duration) {
	if interval < 0 {
		StopSnapshots(instanceName)
		return
	}
	if interval == 0 {
		interval = DefaultSnapshotInterval
	}
	if interval < minSnapshotInterval {
		interval = minSnapshotInterval
	}

	StopSnapshots(instanceName)

	ctx, cancel := context.WithCancel(context.Background())
	runnersMu.Lock()
	runners[instanceName] = cancel
	runnersMu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snapshotOnce(instanceName, mirrorDir)
			}
		}
	}()

	logger.Infof("已为实例 %s 开启插件数据库在线快照，周期 %v", instanceName, interval)
}

// StopSnapshots 停掉某个实例的快照 goroutine。
// 应当在开始停服**之前**调用：saveworld 与进程退出期间的 I/O 不该再被快照争用。
func StopSnapshots(instanceName string) {
	runnersMu.Lock()
	cancel, ok := runners[instanceName]
	delete(runners, instanceName)
	runnersMu.Unlock()
	if ok {
		cancel()
	}
}

// snapshotOnce 为镜像里所有**被识别为 SQLite 的**插件数据库各做一次在线快照。
//
// 不绑定任何具体插件：只要 scanPluginDir 按文件头认出某个文件是 SQLite 库，就为它做。
//
// ⚠️ 绝不能用朴素的定时文件复制来实现这件事。运行期文件组一直在变，
// 逐文件拷会拷出主库与 -wal 互不一致的组合，得到的是**损坏的快照**，比没有更糟。
// 所以必须走 SQLite 自己的在线备份（VACUUM INTO）而不是 fsutil.CopyFile。
func snapshotOnce(instanceName, mirrorDir string) {
	for _, plugin := range listMirrorPlugins(mirrorDir) {
		mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), plugin)
		instPlugin := filepath.Join(InstancePluginsDir(instanceName), plugin)

		if external, path := hasExternalDBPath(instPlugin, mirrorPlugin); external {
			logger.Debugf("插件 %s 的数据库路径由用户接管（%s），跳过快照", plugin, path)
			continue
		}

		for _, g := range scanPluginDir(mirrorPlugin, plugin) {
			if !g.IsSQLite {
				continue
			}
			src := filepath.Join(mirrorPlugin, filepath.FromSlash(g.Base))
			if fi, err := os.Stat(src); err != nil || fi.Size() > maxSnapshotDBBytes {
				if err == nil {
					logger.Warnf(
						"插件 %s 的数据库 %s 有 %d 字节，超过快照上限，本轮跳过",
						plugin, g.Base, fi.Size())
				}
				continue
			}
			dstDir := filepath.Join(instPlugin, snapshotsDirName)
			if err := snapshotDB(src, filepath.Join(dstDir, slashBase(g.Base))); err != nil {
				logger.Warnf("为插件 %s 的 %s 生成快照失败: %v", plugin, g.Base, err)
			}
		}
	}
}

// snapshotDB 用 VACUUM INTO 把一个正在被使用的 SQLite 库导出到 dst。
//
// VACUUM INTO 在一个事务里读源库，产出的是一致的、已 checkpoint 的单文件副本 ——
// 既不需要停服，也不会留下需要一并搬运的 -wal。
//
// 保留一代旧快照（dst.1）：快照写坏（磁盘满、进程被杀）时还有一份能用的。
func snapshotDB(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	tmp := dst + ".tmp"
	// VACUUM INTO 要求目标文件不存在
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}

	// WAL 模式下即便只读也需要对目录有写权限（读者要挂上 -shm 共享索引），
	// 所以这里就按普通读写连接打开。不要试图用 immutable=1 之类的标志绕开 ——
	// 那会跳过 WAL，读到的是过期数据，快照就失去了意义。
	db, err := sql.Open("sqlite", filepath.ToSlash(src)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// VACUUM INTO 的目标只能是字面量，不能用占位符；路径里的单引号需转义。
	stmt := fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(filepath.ToSlash(tmp), "'", "''"))
	if _, err := db.Exec(stmt); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	prev := dst + ".1"
	_ = os.Remove(prev)
	if err := os.Rename(dst, prev); err != nil && !os.IsNotExist(err) {
		logger.Debugf("轮转旧快照 %s 失败: %v", dst, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
