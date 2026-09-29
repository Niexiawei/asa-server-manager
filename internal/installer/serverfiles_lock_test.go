package installer

import (
	"errors"
	"path/filepath"
	"testing"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/filelock"
)

// withTempBaseDir 让 server-files 更新锁落在临时目录，实例目录为空（没有存活实例）。
func withTempBaseDir(t *testing.T) string {
	t.Helper()
	origBase, origInstances := cfgpkg.BaseDir, cfgpkg.InstancesDir
	cfgpkg.BaseDir = t.TempDir()
	cfgpkg.InstancesDir = filepath.Join(cfgpkg.BaseDir, "instances")
	t.Cleanup(func() { cfgpkg.BaseDir, cfgpkg.InstancesDir = origBase, origInstances })
	return filepath.Join(cfgpkg.BaseDir, serverFilesLockName)
}

// 另一个 asa-server 进程（终端里的 update，或服务）正在改写 server-files：
// 本进程既不能开始改写，也必须拒绝启动实例。以前「更新中」只是本进程里的一个布尔，
// 两边互相看不见。锁属于「打开的文件」，在同一进程里直接持有它与另一个进程持有等价。
func TestServerFilesLockSeesOtherProcess(t *testing.T) {
	path := withTempBaseDir(t)

	other, err := filelock.TryLock(path, filelock.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	if !IsUpdatingServerFiles() {
		t.Error("another process's update is invisible to the instance-start check")
	}
	if err := beginServerFilesUpdate(); !errors.Is(err, errServerFilesBusyElsewhere) {
		t.Errorf("beginServerFilesUpdate during another process's update: got %v", err)
	}
	if _, err := BeginArkApiWrite(); !errors.Is(err, errServerFilesBusyElsewhere) {
		t.Errorf("BeginArkApiWrite during another process's update: got %v", err)
	}

	other()
	if IsUpdatingServerFiles() {
		t.Error("still reported as updating after the other process released the lock")
	}
}

// 反方向：本进程在改写时，另一个进程拿不到锁（它的更新会被拒、它的实例启动也会被拒）；
// 结束后锁被放掉——卡住的锁会让所有实例永远起不来。
func TestServerFilesLockHeldAcrossUpdate(t *testing.T) {
	path := withTempBaseDir(t)

	if err := beginServerFilesUpdate(); err != nil {
		t.Fatalf("beginServerFilesUpdate: %v", err)
	}
	if _, err := filelock.TryLock(path, filelock.Shared); !errors.Is(err, filelock.ErrLocked) {
		t.Errorf("another process could take the lock during our update: %v", err)
	}
	endServerFilesUpdate()

	release, err := filelock.TryLock(path, filelock.Exclusive)
	if err != nil {
		t.Fatalf("lock not released after endServerFilesUpdate: %v", err)
	}
	release()
}
