import {ref, watch, onMounted, onBeforeUnmount} from 'vue'
import {getMetricsHistory} from '@/apis/api.js'
import {subscribeHost, subscribeInstance} from '@/composables/useResourceStream.js'

/**
 * 趋势图的数据缓冲。
 *
 * 挂载时先 GET 回填，再订阅实时流 —— 顺序不能反：SharedWorker 全浏览器只有一条
 * all-info 长连接，中途挂载的面板收不到"首帧"，所以历史只能靠独立接口拿。
 * 两者的重叠帧由「时间戳必须严格递增」这条规则自动去掉。
 */

// 采样与推送都是 2 秒；最大渲染窗口 15 分钟 = 450 点
const TICK_MS = 2000
const MAX_POINTS = 450
const BACKFILL_SECONDS = 900

// 本地补点的判定：超过这么久没收到帧，就认为这一 tick 是空的
const STALE_MS = 3000

const HOST_FIELDS = [
    'cpu_used_percent',
    'mem_used_percent',
    'disk_read_bytes_per_sec',
    'disk_write_bytes_per_sec',
    'disk_read_iops',
    'disk_write_iops',
    'net_recv_bytes_per_sec',
    'net_sent_bytes_per_sec',
]

const INSTANCE_FIELDS = [
    'cpu_percent',
    'cpu_total_percent',
    'memory_percent',
    'memory_used',
    'disk_read_bytes_per_sec',
    'disk_write_bytes_per_sec',
    'disk_read_iops',
    'disk_write_iops',
    'net_recv_bytes_per_sec',
    'net_sent_bytes_per_sec',
]

const num = (v) => (typeof v === 'number' && isFinite(v) ? v : null)

function hostPoint(payload) {
    const h = payload?.host || {}
    return {
        cpu_used_percent: num(h.cpu?.used_percent),
        mem_used_percent: num(h.memory?.used_percent),
        disk_read_bytes_per_sec: num(h.disk_io?.read_bytes_per_sec),
        disk_write_bytes_per_sec: num(h.disk_io?.write_bytes_per_sec),
        disk_read_iops: num(h.disk_io?.read_iops),
        disk_write_iops: num(h.disk_io?.write_iops),
        net_recv_bytes_per_sec: num(h.net_io?.recv_bytes_per_sec),
        net_sent_bytes_per_sec: num(h.net_io?.sent_bytes_per_sec),
    }
}

function instancePoint(payload) {
    const p = payload?.process || {}
    // disk_io / net_io 整块为 null 表示"采不到"，与"速率是 0"不是一回事，
    // 前者要画成断点，所以这里保持 null 而不是折成 0
    const d = payload?.disk_io || null
    const n = payload?.net_io || null
    return {
        cpu_percent: num(p.cpu_percent),
        cpu_total_percent: num(p.cpu_total_percent),
        memory_percent: num(p.memory_percent),
        memory_used: num(p.memory_used),
        disk_read_bytes_per_sec: d ? num(d.read_bytes_per_sec) : null,
        disk_write_bytes_per_sec: d ? num(d.write_bytes_per_sec) : null,
        disk_read_iops: d ? num(d.read_iops) : null,
        disk_write_iops: d ? num(d.write_iops) : null,
        net_recv_bytes_per_sec: n ? num(n.recv_bytes_per_sec) : null,
        net_sent_bytes_per_sec: n ? num(n.sent_bytes_per_sec) : null,
    }
}

/**
 * @param {Object} opts
 * @param {'host'|'instance'} opts.scope
 * @param {import('vue').Ref<string>|string} [opts.instanceName]
 * @param {import('vue').Ref<boolean>} [opts.enabled] 为 false 时不回填也不订阅；
 *        缺省视为恒 true。首页 masonry 下每个实例卡片都挂一份，未运行的实例
 *        没必要在页面加载时各发一次历史回填请求。
 * @param {number} [opts.backfillSeconds] 回填窗口，缺省 900（15 分钟）。
 *        迷你 sparkline 只画几分钟，传小一点能显著缩小首屏那几个请求的体积。
 */
export function useResourceTrend(opts) {
    const scope = opts.scope
    const fields = scope === 'host' ? HOST_FIELDS : INSTANCE_FIELDS
    const instanceName = () => (typeof opts.instanceName === 'string' ? opts.instanceName : opts.instanceName?.value)
    const enabled = opts.enabled
    const backfillSeconds = opts.backfillSeconds ?? BACKFILL_SECONDS

    // 缓冲用普通数组而不是 ref：450 点 × 10 条曲线每 2 秒全量代理一遍纯属浪费，
    // 变更用一个计数器通知，切片时读它来建立依赖
    const version = ref(0)
    const ready = ref(false)
    const ts = []
    const cols = {}
    fields.forEach(f => (cols[f] = []))

    let lastFrameAt = 0
    let timer = null
    let unsubscribe = null
    let disposed = false

    const trim = () => {
        if (ts.length <= MAX_POINTS) return
        const drop = ts.length - MAX_POINTS
        ts.splice(0, drop)
        fields.forEach(f => cols[f].splice(0, drop))
    }

    const appendPoint = (tsSec, point) => {
        ts.push(tsSec)
        fields.forEach(f => cols[f].push(point ? (point[f] ?? null) : null))
    }

    // 时间轴必须严格递增：连接建立与断线重连时后端都会先补一帧 immediate，
    // 它的时间戳可能与缓冲末点相同甚至更早，喂给 uPlot 会画乱
    const push = (tsSec, point) => {
        if (!(tsSec > 0)) return
        const last = ts.length ? ts[ts.length - 1] : 0
        if (last && tsSec <= last) return

        // 时间上有断档（服务重启、断线重连）时先插一个空点，
        // 否则 uPlot 会把断档两端直接连成一条斜线，看上去像"那几分钟一直在缓慢变化"
        if (last && tsSec - last > (2 * TICK_MS) / 1000) {
            appendPoint(last + TICK_MS / 1000, null)
        }

        appendPoint(tsSec, point)
        trim()
        version.value++
    }

    const onFrame = (payload) => {
        if (disposed || !payload) return
        lastFrameAt = Date.now()
        const tsSec = Number(payload.timestamp)
        push(tsSec, scope === 'host' ? hostPoint(payload) : instancePoint(payload))
    }

    // 实例停机后 all-info 不再输出它，曲线会"冻结"而不是走出空洞；
    // 这里按 tick 补 null 点，让时间轴继续前进，图上能看出是从哪一刻断的
    const startPadding = () => {
        if (timer) return
        timer = setInterval(() => {
            if (disposed) return
            if (Date.now() - lastFrameAt < STALE_MS) return
            push(Math.floor(Date.now() / 1000), null)
        }, TICK_MS)
    }

    const backfill = async () => {
        try {
            const res = await getMetricsHistory(backfillSeconds, scope === 'instance' ? instanceName() : undefined)
            if (disposed || !res || !Array.isArray(res.timestamps)) return
            const src = scope === 'host' ? res.host : res.instance
            const stamps = res.timestamps
            for (let i = 0; i < stamps.length; i++) {
                // 实例可能还没有历史（刚创建/从没跑过），此时只铺时间轴，曲线全空。
                // 走 push 而不是直接 append：断档补空点与单调性检查都在那里
                const point = {}
                fields.forEach(f => {
                    const col = src?.[f]
                    point[f] = col && col[i] != null ? col[i] : null
                })
                push(Number(stamps[i]), point)
            }
        } catch (err) {
            // 回填失败不影响实时曲线，只是没有历史
            console.warn('[ResourceTrend] 历史回填失败:', err)
        } finally {
            ready.value = true
        }
    }

    /**
     * 取最近 windowSeconds 的数据，组装成 uPlot 要的 [xs, ...ys]。
     * @param {string[]} names 曲线字段名，顺序与 series 配置一致
     * @param {number} windowSeconds
     */
    const slice = (names, windowSeconds) => {
        version.value // 建立响应式依赖
        if (ts.length === 0) return [[], ...names.map(() => [])]

        // 按**时间**而不是点数截取：中间有断档（重启、断线）时按点数取会让
        // "6 分钟"实际跨出十几分钟，与分段控件上写的字对不上
        const cutoff = ts[ts.length - 1] - windowSeconds
        let start = 0
        for (let i = ts.length - 1; i >= 0; i--) {
            if (ts[i] < cutoff) {
                start = i + 1
                break
            }
        }
        return [ts.slice(start), ...names.map(n => (cols[n] || []).slice(start))]
    }

    /** 某条曲线在当前缓冲里有没有过真实数据（用来决定画图还是画"不支持"占位） */
    const hasData = (name) => {
        version.value
        const col = cols[name] || []
        for (let i = col.length - 1; i >= 0; i--) {
            if (col[i] != null) return true
        }
        return false
    }

    let started = false
    // 代次计数：stop→start 若发生在回填还没返回的那一瞬间，只靠 started 布尔量
    // 判断会让**两次** start 的续段都通过检查，于是订阅两次、漏掉一次退订
    let runId = 0

    const start = async () => {
        if (started || disposed) return
        started = true
        const myRun = ++runId
        // 顺序固定：先回填灌满缓冲，再订阅增量
        await backfill()
        if (disposed || runId !== myRun) return
        lastFrameAt = Date.now()
        unsubscribe = scope === 'host'
            ? subscribeHost(onFrame)
            : subscribeInstance(instanceName(), onFrame)
        startPadding()
    }

    // 停下来不清缓冲：实例停了再起来时曲线应当接着画，中间那段由 push 的
    // 断档规则补一个空点，图上看得出是从哪一刻断的
    const stop = () => {
        started = false
        runId++
        if (timer) {
            clearInterval(timer)
            timer = null
        }
        if (unsubscribe) {
            unsubscribe()
            unsubscribe = null
        }
    }

    onMounted(() => {
        if (!enabled) {
            start()
            return
        }
        watch(enabled, (v) => (v ? start() : stop()), {immediate: true})
    })

    onBeforeUnmount(() => {
        disposed = true
        stop()
    })

    return {ready, version, slice, hasData}
}
