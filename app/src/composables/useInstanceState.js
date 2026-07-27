import {serverStore, getCountdown, formatCountdown} from '@/store/serverStore.js'

const CLEAN_STOPPED = ['stopped', 'start_failed', 'stop_failed', 'restart_failed', '']
const START_LOADING_STATES = ['start_initialization', 'start_initialization_successful', 'starting']
const SHOULD_MONITOR_LOGS = ['start_initialization', 'start_initialization_successful', 'starting', 'started', 'stopping', 'restarting', 'restarted']

export const STATUS_LABELS = {
    start_initialization: '初始化中',
    start_initialization_successful: '初始化完成',
    starting: '启动中',
    started: '运行中',
    stopping: '停止中',
    stopped: '已停止',
    restarting: '重启中',
    restarted: '运行中',
    start_failed: '启动失败',
    stop_failed: '停止失败',
    restart_failed: '重启失败',
}

export const STATUS_TAG_THEMES = {
    start_initialization: 'primary',
    start_initialization_successful: 'primary',
    starting: 'warning',
    started: 'success',
    stopping: 'warning',
    stopped: 'default',
    restarting: 'warning',
    restarted: 'success',
    start_failed: 'danger',
    stop_failed: 'danger',
    restart_failed: 'danger',
}

export function statusLabel(status) {
    return STATUS_LABELS[status] || '已停止'
}

export function statusTagTheme(status) {
    return STATUS_TAG_THEMES[status] || 'default'
}

export function isShouldMonitorLogs(status) {
    return SHOULD_MONITOR_LOGS.includes(status)
}

export function isCleanStoppedStatus(status) {
    return CLEAN_STOPPED.includes(status)
}

export function canStart(status) {
    return isCleanStoppedStatus(status)
}

export function canStop(status) {
    return status === 'started'
}

export function canRestart(status) {
    return status === 'started'
}

export function canForceStop(status) {
    return status !== 'stopped' && status !== ''
}

export function isStartLoading(name, status) {
    return !serverStore.restartPending.has(name)
        && START_LOADING_STATES.includes(status)
}

export function isStopLoading(status) {
    return status === 'stopping'
}

export function isRestartLoading(name, status) {
    return serverStore.restartPending.has(name) || status === 'restarting'
}

// 倒计时展示：counting 显示剩余时间，executing 显示「服务器关闭中…」。
// phase 由后端给出，不靠 remaining <= 0 推断——执行阶段可能持续几分钟
export function countdownText(instanceName) {
    const cd = getCountdown(instanceName)
    if (!cd) return ''

    const label = cd.action === 'restart' ? '重启' : '关闭'
    if (cd.phase === 'executing') return `服务器${label}中…`
    return `${formatCountdown(cd.remaining)}后${label}`
}

export function isCountingDown(instanceName) {
    return getCountdown(instanceName)?.phase === 'counting'
}

export function useInstanceState(instanceNameRef) {
    const canStartInstance = (status) => canStart(status)
    const canStopInstance = (status) => canStop(status)
    const canRestartInstance = (status) => canRestart(status)

    const isStartLoadingInstance = (status) => isStartLoading(instanceNameRef.value, status)
    const isRestartLoadingInstance = (status) => isRestartLoading(instanceNameRef.value, status)

    return {
        canStartInstance,
        canStopInstance,
        canRestartInstance,
        canForceStop,
        isStartLoadingInstance,
        isStopLoading,
        isRestartLoadingInstance,
        statusLabel,
        statusTagTheme,
        isCleanStoppedStatus,
    }
}
