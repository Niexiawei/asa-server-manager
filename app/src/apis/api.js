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

// 把倒计时选项转成 query 参数。countdown 为空/0 时返回空对象，
// 请求形态与加倒计时之前完全一致。
function countdownParams(countdown) {
    if (!countdown || !countdown.countdown) return {}

    const params = {countdown: countdown.countdown}
    if (countdown.notify_points?.length) {
        params.notify_points = countdown.notify_points.join(',')
    }
    if (countdown.notify_message) params.notify_message = countdown.notify_message
    if (countdown.notify_command) params.notify_command = countdown.notify_command
    return params
}

// 停止服务器实例。countdown 可选：{countdown, notify_points, notify_message, notify_command}
export function stopServer(name, countdown) {
    return apiClient.get(`/api/server/${name}/stop`, {params: countdownParams(countdown)})
}

// 重启服务器实例。countdown 同上
export function restartServer(name, countdown) {
    return apiClient.get(`/api/server/${name}/restart`, {params: countdownParams(countdown)})
}

// 查询实例当前的倒计时状态（页面首次加载时补状态，WS 之外的兜底）
export function getServerCountdown(name) {
    return apiClient.get(`/api/server/${name}/countdown`)
}

// 取消实例的停止/重启倒计时
export function cancelServerCountdown(name) {
    return apiClient.post(`/api/server/${name}/countdown/cancel`)
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

// 更新 Game.ini 配置文件内容（直接通过文本）
export function updateGameIni(instanceName, content) {
    return apiClient.put(`/api/config/${instanceName}/game-ini`, {content})
}

// 更新 GameUserSettings.ini 配置文件内容（直接通过文本）
export function updateGameUserSettings(instanceName, content) {
    return apiClient.put(`/api/config/${instanceName}/game-user-settings`, {content})
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

// 把倒计时选项转成批量请求体字段。与 countdownParams 的区别是
// 批量走 JSON body，notify_points 保持数组而不是逗号串。
// countdown 为空/0 时返回空对象，请求形态与加倒计时之前完全一致。
function batchCountdownBody(countdown) {
    if (!countdown?.countdown) return {}

    return {
        countdown: countdown.countdown,
        notify_points: countdown.notify_points || [],
        notify_message: countdown.notify_message || '',
        notify_command: countdown.notify_command || ''
    }
}

// 批量启动服务器。启动没有倒计时——服务器是关着的，无人可公告
export function batchStartServers(instances, delaySeconds) {
    return apiClient.post('/api/server/batch/start', {
        instances: instances || [],
        delay_seconds: delaySeconds || 0
    })
}

// 批量停止服务器。countdown 可选：{countdown, notify_points, notify_message, notify_command}
export function batchStopServers(instances, delaySeconds, countdown) {
    return apiClient.post('/api/server/batch/stop', {
        instances: instances || [],
        delay_seconds: delaySeconds || 0,
        ...batchCountdownBody(countdown)
    })
}

// 批量重启服务器。countdown 同上
export function batchRestartServers(instances, delaySeconds, countdown) {
    return apiClient.post('/api/server/batch/restart', {
        instances: instances || [],
        delay_seconds: delaySeconds || 0,
        ...batchCountdownBody(countdown)
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

// 获取执行日志。taskId 为空表示全部任务；limit 缺省 100
export function listScheduleLogs(taskId, limit) {
    return apiClient.get('/api/schedule/logs', {
        params: {
            task_id: taskId || undefined,
            limit: limit || undefined,
        },
    })
}

// 清空执行日志
export function clearScheduleLogs() {
    return apiClient.delete('/api/schedule/logs')
}

// 获取当前正在执行的任务，供任务列表页渲染「取消」按钮与执行阶段标签
export function listScheduleRuns() {
    return apiClient.get('/api/schedule/runs')
}

// 取消一次正在执行的任务。状态回滚（把执行前活着的实例拉回来）由后端完成，
// 这里立即返回
export function cancelScheduleRun(runId) {
    return apiClient.post(`/api/schedule/runs/${runId}/cancel`)
}

// ==================== 待恢复实例 ====================

// 查询「定时更新后未恢复启动」的现场，无现场时 data.pending 为 null
export function getPendingRestore() {
    return apiClient.get('/api/schedule/pending-restore')
}

// 确认恢复：后台批量启动，接口立即返回
export function confirmPendingRestore() {
    return apiClient.post('/api/schedule/pending-restore/confirm')
}

// 忽略：删除现场记录，不再提示（实例保持停止状态）
export function ignorePendingRestore() {
    return apiClient.delete('/api/schedule/pending-restore')
}

// ==================== ArkApi 插件数据隔离 ====================
// 插件的配置与运行期数据（典型是 Permissions 的权限库）按实例隔离存放在
// instances/{name}/plugins/ 下，启动前注入镜像、停止后回收。
// 详见 docs/ARKAPI_PLUGIN_DATA_PLAN.md。

// 列出某实例的插件隔离状态（是否已隔离、数据文件、快照、是否被 DbPathOverride 接管）
export function listInstancePlugins(name) {
    return apiClient.get(`/api/plugins/${name}`)
}

// 读取插件配置。响应里的 seeded=false 表示实例侧还没有独立配置，
// 当前展示的是源服务端自带的默认值，保存后才成为本实例的配置。
export function getPluginConfig(name, plugin) {
    return apiClient.get(`/api/plugins/${name}/${plugin}/config`)
}

// 保存插件配置（写入实例目录，下次启动该实例时注入镜像生效）
export function updatePluginConfig(name, plugin, content) {
    return apiClient.put(`/api/plugins/${name}/${plugin}/config`, {content})
}
