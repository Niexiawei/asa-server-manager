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

// 停止服务器实例 (非 SSE)
export function stopServer(name) {
    return apiClient.get(`/api/server/${name}/stop`)
}

// 发送 RCON 命令
export function sendRCONCommand(name, command) {
    return apiClient.post(`/api/rcon/${name}/command`, {command})
}

// 创建备份
export function createBackup(name) {
    return apiClient.post(`/api/backup/${name}`, {})
}

// 列出所有备份
export function listBackups() {
    return apiClient.get('/api/backup')
}

// 恢复备份（可选择恢复的内容）
export function restoreBackup(name, backupFile, options = {}) {
    const requestBody = {
        backup_file: backupFile
    }

    // 如果提供了选项参数，添加到请求体
    if (options.restoreWorldfile !== undefined) {
        requestBody.restore_worldfile = options.restoreWorldfile
    }
    if (options.restoreInstanceConfig !== undefined) {
        requestBody.restore_instance_config = options.restoreInstanceConfig
    }
    if (options.restoreGameConfig !== undefined) {
        requestBody.restore_game_config = options.restoreGameConfig
    }

    return apiClient.post(`/api/backup/${name}/restore`, requestBody)
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

// 同步游戏配置（Game.ini 和 GameUserSettings.ini）
export function syncGameConfig(instances) {
    return apiClient.post('/api/config/sync', {instances})
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
