// API 服务封装
import apiClient, { API_BASE_URL } from '@/utils/http'
// 导出所有 SSE 相关 API
export * from './sseApi'

export { API_BASE_URL }

// 健康检查
export function healthCheck() {
    return apiClient.get('/health')
}

// 获取实例列表
export function listInstances() {
    return apiClient.get('/api/instances')
}

// 创建实例
export function createInstance(name) {
    return apiClient.post('/api/instances', {name})
}

// 获取实例状态
export function getInstanceStatus(name) {
    return apiClient.get(`/api/instances/${name}`)
}

// 获取实例完整配置
export function getInstanceConfig(name) {
    return apiClient.get(`/api/instances/${name}`)
}

// 更新实例配置
export function updateInstanceConfig(name, config) {
    return apiClient.patch(`/api/instances/${name}/config`, config)
}

// 删除实例
export function deleteInstance(name) {
    return apiClient.delete(`/api/instances/${name}`)
}

// 重命名实例
export function renameInstance(name, newName) {
    return apiClient.put(`/api/instances/${name}`, {new_name: newName})
}

// 启动服务器实例
export function startServer(name) {
    return apiClient.get(`/api/server/${name}/start`)
}

// 停止服务器实例
export function stopServer(name) {
    return apiClient.get(`/api/server/${name}/stop`)
}

// 重启服务器实例
export function restartServer(name) {
    return apiClient.get(`/api/server/${name}/restart`)
}

// 强制停止服务器实例
export function forceStopServer(name) {
    return apiClient.get(`/api/server/${name}/force-stop`)
}

// 创建世界存档备份
export function createWorldBackup(name) {
    return apiClient.post(`/api/backup/world/${name}`, {})
}

// 列出指定实例的世界存档备份
export function listWorldBackups(name) {
    return apiClient.get(`/api/backup/world/${name}`)
}

// 恢复世界存档备份
export function restoreWorldBackup(name, backupFile) {
    return apiClient.post(`/api/backup/world/${name}/restore`, {
        backup_file: backupFile
    })
}

// 删除世界存档备份文件
export function deleteWorldBackup(name, filename) {
    return apiClient.delete(`/api/backup/world/${name}/${encodeURIComponent(filename)}`)
}

// 获取 Game.ini 配置文件内容
export function getGameIni(instanceName) {
    return apiClient.get(`/api/config/${instanceName}/game-ini`)
}

// 获取 GameUserSettings.ini 配置文件内容
export function getGameUserSettings(instanceName) {
    return apiClient.get(`/api/config/${instanceName}/game-user-settings`)
}

// 获取服务器基础配置文件（Game.ini 和 GameUserSettings.ini）
export function getServerConfigs() {
    return apiClient.get('/api/config/server/configs')
}

// 获取实例配置文件（Game.ini 和 GameUserSettings.ini）
export function getInstanceConfigs(instanceName) {
    return apiClient.get(`/api/config/${instanceName}/configs`)
}


// 更新 Game.ini 配置文件内容（直接通过文本）
export function updateGameIni(instanceName, content) {
    return apiClient.put(`/api/config/${instanceName}/game-ini`, {content})
}

// 更新 GameUserSettings.ini 配置文件内容（直接通过文本）
export function updateGameUserSettings(instanceName, content) {
    return apiClient.put(`/api/config/${instanceName}/game-user-settings`, {content})
}

// 上传 Game.ini 文件（FormData 方式）
export function uploadGameIniFile(instanceName, file) {
    const formData = new FormData()
    formData.append('file', file)
    return apiClient.post(`/api/config/${instanceName}/game-ini`, formData)
}

// 上传 GameUserSettings.ini 文件（FormData 方式）
export function uploadGameUserSettingsFile(instanceName, file) {
    const formData = new FormData()
    formData.append('file', file)
    return apiClient.post(`/api/config/${instanceName}/game-user-settings`, formData)
}

// 同步实例配置
export function syncInstanceConfig(sourceInstance, targetInstances, syncCustomStartParameters, syncEnableAsaPlugin) {
    return apiClient.post('/api/config/sync-instance', {
        source_instance: sourceInstance,
        target_instances: targetInstances,
        sync_custom_start_parameters: syncCustomStartParameters,
        sync_enable_asa_plugin: syncEnableAsaPlugin,
    })
}

// FRP 管理接口
// 获取 FRP 配置
export function getFRPConfig() {
    return apiClient.get('/api/frp/config')
}

// 更新 FRP 配置
export function updateFRPConfig(config) {
    return apiClient.put('/api/frp/config', {config})
}

// 获取 FRP 状态
export function getFRPStatus() {
    return apiClient.get('/api/frp/status')
}

export function startFRP() {
    return apiClient.post('/api/frp/start')
}

// 停止 FRP
export function stopFRP() {
    return apiClient.post('/api/frp/stop')
}

// 重启 FRP
export function restartFRP() {
    return apiClient.post('/api/frp/restart')
}


// Syncthing 管理接口
// 获取 Syncthing 配置
export function getSyncthingConfig() {
    return apiClient.get('/api/syncthing/config')
}

// 更新 Syncthing 配置
export function updateSyncthingConfig(config) {
    return apiClient.put('/api/syncthing/config', {config})
}

// 获取 Syncthing 状态
export function getSyncthingStatus() {
    return apiClient.get('/api/syncthing/status')
}

// 启动 Syncthing
export function startSyncthing() {
    return apiClient.post('/api/syncthing/start')
}

// 停止 Syncthing
export function stopSyncthing() {
    return apiClient.post('/api/syncthing/stop')
}

// 重启 Syncthing
export function restartSyncthing() {
    return apiClient.post('/api/syncthing/restart')
}

export function getModInfo() {
    return apiClient.get('/api/mod-info')
}

// ========== 批量操作 API ==========

// 批量启动服务器
export function batchStartServers(instances, delaySeconds) {
    return apiClient.post('/api/server/batch/start', {
        instances: instances || [],
        delay_seconds: delaySeconds || 0
    })
}

// 批量停止服务器
export function batchStopServers(instances, delaySeconds) {
    return apiClient.post('/api/server/batch/stop', {
        instances: instances || [],
        delay_seconds: delaySeconds || 0
    })
}

// 批量重启服务器
export function batchRestartServers(instances, delaySeconds) {
    return apiClient.post('/api/server/batch/restart', {
        instances: instances || [],
        delay_seconds: delaySeconds || 0
    })
}

// 获取批量操作状态
export function getBatchStatus() {
    return apiClient.get('/api/server/batch/status')
}

// 取消当前批量操作
export function cancelBatch() {
    return apiClient.post('/api/server/batch/cancel')
}

// 跳过批量操作中的指定实例
export function skipBatchInstance(instanceName) {
    return apiClient.post('/api/server/batch/skip', {instance_name: instanceName})
}

// 获取服务器更新状态
export function getUpdateStatus() {
    return apiClient.get('/api/server/update/status')
}

// 取消服务器更新
export function cancelUpdate() {
    return apiClient.post('/api/server/update/cancel')
}

// ==================== 定时任务 ====================

// 获取定时任务列表
export function listScheduleTasks() {
    return apiClient.get('/api/schedule/tasks')
}

// 新建定时任务
export function createScheduleTask(task) {
    return apiClient.post('/api/schedule/tasks', task)
}

// 修改定时任务
export function updateScheduleTask(id, task) {
    return apiClient.put(`/api/schedule/tasks/${id}`, task)
}

// 删除定时任务
export function deleteScheduleTask(id) {
    return apiClient.delete(`/api/schedule/tasks/${id}`)
}

// 启用/停用定时任务
export function toggleScheduleTask(id, enabled) {
    return apiClient.post(`/api/schedule/tasks/${id}/toggle`, {enabled})
}

// 立即执行一次定时任务
export function runScheduleTaskNow(id) {
    return apiClient.post(`/api/schedule/tasks/${id}/run`)
}
