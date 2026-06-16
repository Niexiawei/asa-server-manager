package asaserverv2

import (
	"asa-server/asaserver"
	"asa-server/logger"
)

// ForceStopServer 强制停止服务器实例
// 不等待优雅关闭，直接杀死进程并清理资源
func ForceStopServer(instanceName string) error {
	// 1. 通过端口查找进程并杀死
	cfg, err := asaserver.LoadInstanceConfig(instanceName)
	if err == nil {
		if pid, pidErr := findServerPIDByPort(cfg.Port); pidErr == nil && pid > 0 {
			killGameServer(pid)
		}
	}
	// 2. 尝试杀死已保存的 PID（插件进程）
	if pid2, pidErr := asaserver.GetInstancePID(instanceName); pidErr == nil && pid2 > 0 {
		killGameServer(pid2)
	}
	// 3. 安全释放锁（仅在持有锁时释放）
	if asaserver.TryLockServerActions() {
		asaserver.UnlockServerActions()
	}
	// 4. 重置状态为 stopped
	_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStopped, "")
	// 5. 清理镜像目录
	if err := CleanupInstanceMirror(instanceName); err != nil {
		logger.GetLogger().Warnf("Failed to cleanup instance mirror for %s: %v", instanceName, err)
	}
	return nil
}
