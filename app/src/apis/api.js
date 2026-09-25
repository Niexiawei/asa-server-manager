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
//
// 收的是结构化参数而不是配置文件文本：
// { server_addr, server_port, token, rules: [{start, end, protocol, remark}] }
export function updateFRPConfig(config) {
    return apiClient.put('/api/frp/config', config)
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

// 集群文件同步（simple-file-sync）管理接口，见 docs/FILESYNC_REPLACE_SYNCTHING_PLAN.md §8 P3
//
// GET 返回的配置不含任何 PEM：
// { configured, enabled, address, label, has_ca, has_bootstrap,
//   credential: { address, ca_fingerprint, cert_not_after } | null,
//   upload_limit_kbps, download_limit_kbps, clusters: [{cluster_id}] }
export function getFileSyncConfig() {
    return apiClient.get('/api/filesync/config')
}

// 保存配置。凭据二选一：join_blob，或 ca_pem / bootstrap_cert_pem / bootstrap_key_pem（留空表示不修改）。
export function updateFileSyncConfig(config) {
    return apiClient.put('/api/filesync/config', config)
}

// 解开接入字符串但不保存，返回 { address, ca_fingerprint, cert_not_after }
export function inspectJoinBlob(joinBlob) {
    return apiClient.post('/api/filesync/join-blob/inspect', {join_blob: joinBlob})
}

export function getFileSyncStatus() {
    return apiClient.get('/api/filesync/status')
}

export function startFileSync() {
    return apiClient.post('/api/filesync/start')
}

export function stopFileSync() {
    return apiClient.post('/api/filesync/stop')
}

export function restartFileSync() {
    return apiClient.post('/api/filesync/restart')
}

// 删除本机节点证书，用引导凭据重新接入（协调端要先对本机执行 node reset）
export function resetFileSyncIdentity() {
    return apiClient.post('/api/filesync/identity/reset')
}

// 各实例配置里已有的 ClusterID：[{ cluster_id, instances: [] }]
export function getFileSyncClusters() {
    return apiClient.get('/api/filesync/clusters')
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

// ==================== ArkApi 插件（每实例独立） ====================
// 每个实例的插件（dll、配置、运行期数据）都在 instances/{name}/ArkApi/Plugins/ 下，
// 镜像里的 ArkApi/Plugins 是指向它的 junction。详见 docs/ARKAPI_PLUGIN_INSTALL_PLAN.md。

// 列出某实例的插件：元数据、启用状态、数据文件、快照、是否被 DbPathOverride 接管
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

// 启用/禁用某实例的一个插件，只作用于这一个实例。
// 实例运行中或正在启动时只写配置，响应 data.applied=false，下次启动时生效。
export function setInstancePluginEnabled(name, plugin, enabled) {
    return apiClient.put(`/api/plugins/${name}/${encodeURIComponent(plugin)}/enabled`, {enabled})
}

// 上传 ArkApi 包（两段式的第一段）：服务端解压并校验，返回报告与目标实例表，token 30 分钟有效。
// 校验失败是 422，错误对象的 data 是同样形状的报告（token 为空）。
export function uploadArkApiPackage(kind, file, {expect, onProgress} = {}) {
    const form = new FormData()
    form.append('file', file)
    return apiClient.post('/api/arkapi/packages', form, {
        params: expect ? {kind, expect} : {kind},
        // 默认头是 application/json，axios 1.x 在这个头下会把 FormData 序列化成 JSON
        headers: {'Content-Type': 'multipart/form-data'},
        onUploadProgress: (e) => onProgress?.(e.total ? Math.round((e.loaded * 100) / e.total) : 0),
    })
}

// 确认安装。结果按实例分别报告。
// 插件包：{targets: ['a', 'b'], restore_from_backup: true}，targets 必须显式给出；
// 主程序包：{version: '2.03', bundled: {Permissions: ['a']}}，附带插件不列就不装
export function applyArkApiPackage(token, body) {
    return apiClient.post(`/api/arkapi/packages/${token}/apply`, body)
}

// 放弃暂存的包（关掉确认对话框时调用）
export function discardArkApiPackage(token) {
    return apiClient.delete(`/api/arkapi/packages/${token}`)
}

// 装有某插件的全部实例（含禁用状态的），供卸载对话框使用
export function getPluginInstances(plugin) {
    return apiClient.get(`/api/arkapi/plugins/${encodeURIComponent(plugin)}/instances`)
}

// 从所选实例卸载插件。targets 必须显式给出，接口不存在「默认全部」
export function uninstallPlugin(plugin, targets) {
    return apiClient.post(`/api/arkapi/plugins/${encodeURIComponent(plugin)}/uninstall`, {targets})
}

// ArkApi 主程序状态：是否安装、版本（本程序装的才知道）、安装后被外部改动的文件、server-files 是否正忙
export function getArkApiStatus() {
    return apiClient.get('/api/arkapi')
}

// 卸载 ArkApi 主程序（全局，影响所有实例）。各实例的插件目录不动，被覆盖过的游戏文件会还原
export function uninstallArkApi() {
    return apiClient.delete('/api/arkapi')
}

// ==================== 资源指标历史 ====================
// 趋势图挂载时先拉这个把缓冲灌满，再订阅实时流（SharedWorker 只有一条长连接，
// 中途挂载的面板收不到"首帧"，所以回填只能走独立接口）。
// window 单位为秒，缺省 900（15 分钟），上限 1800；不传 instance 只回 host。
export function getMetricsHistory(windowSeconds, instance) {
    return apiClient.get('/api/server/metrics/history', {
        params: {window: windowSeconds, instance: instance || undefined}
    })
}
