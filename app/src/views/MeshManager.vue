<template>
  <!-- 远程管理器（管理器互控，docs/REMOTE_MANAGER_MESH_PLAN.md §7.3、§12 P4-4）。只在本机上下文出现。 -->
  <t-card class="mesh-card layout-card" :bordered="false">
    <template #title>
      <div class="mesh-header">
        <span class="page-title">远程管理器</span>
        <!-- 启停只看 mesh 自己是否在运行；有没有协调节点不影响（无协调节点时按手填地址直连） -->
        <t-tag v-if="status.running" theme="success" variant="light">运行中</t-tag>
        <t-tag v-else-if="status.last_error" theme="danger" variant="light" :title="status.last_error">启动失败</t-tag>
        <t-tag v-else variant="light">已停止</t-tag>
        <span class="spacer"/>
        <t-button size="small" theme="primary" @click="lifecycle('start')" :disabled="status.running"
                  :loading="busy.life === 'start'">启动
        </t-button>
        <t-popconfirm content="停止后所有远程连接断开，对方也无法再控制本机。" @confirm="lifecycle('stop')">
          <t-button size="small" theme="danger" :disabled="!status.running" :loading="busy.life === 'stop'">停止
          </t-button>
        </t-popconfirm>
        <t-button size="small" theme="warning" @click="lifecycle('restart')" :disabled="!status.running"
                  :loading="busy.life === 'restart'">重启
        </t-button>
        <t-button size="small" variant="outline" @click="reloadAll" :loading="loading">刷新</t-button>
      </div>
    </template>

    <div class="mesh-body">
      <t-alert v-if="!authState.authEnabled" theme="warning" class="block-alert"
               message="本机没有开启登录鉴权：任何能打开本页面的人都能控制已配对的机器。"/>
      <t-alert v-if="status.last_error" theme="error" class="block-alert" :message="`最近的错误：${status.last_error}`"/>

      <!-- ① 本机 -->
      <section class="section">
        <h3>本机</h3>
        <div class="kv">
          <span class="k">节点 ID</span>
          <span class="v mono">{{ status.node_id || '尚未生成（首次启动时生成）' }}</span>
          <t-button v-if="status.node_id" size="small" variant="text" @click="copy(status.node_id)">复制</t-button>
        </div>
        <div class="kv">
          <span class="k">Peer 端口</span>
          <span class="v">
            <template v-if="status.no_listen">不监听（只能经中转被连接）</template>
            <template v-else-if="status.listen_addr">监听 {{ status.listen_addr }}</template>
            <template v-else>{{ status.peer_port }}（未运行）</template>
            <span v-if="status.listen_error" class="err">{{ status.listen_error }}</span>
          </span>
        </div>
        <div class="kv" v-if="status.candidates?.length">
          <span class="k">直连地址</span>
          <span class="v mono">{{ status.candidates.join('，') }}</span>
        </div>
        <div class="kv">
          <span class="k">版本</span>
          <span class="v">{{ status.version }}</span>
        </div>

        <t-divider>本机设置</t-divider>
        <t-form label-width="130px" :data="form" class="cfg-form">
          <t-form-item label="备注名">
            <t-input v-model="form.label" placeholder="对方看到的本机名字，例如「机房-1」" style="max-width: 320px"/>
          </t-form-item>
          <t-form-item label="监听 Peer 端口">
            <t-switch v-model="form.listen"/>
            <t-input-number v-if="form.listen" v-model="form.peer_port" :min="1" :max="65535" theme="normal"
                            style="width: 140px; margin-left: 12px"/>
            <span class="muted note">用于内网 / 公网直连。Windows 首次监听会弹防火墙提示；不放行时会自动走中转。</span>
          </t-form-item>
          <t-form-item label="允许打洞">
            <t-switch v-model="form.punch"/>
            <t-input-number v-if="form.punch" v-model="form.udp_port" :min="1" :max="65535" theme="normal"
                            placeholder="同 Peer 端口" style="width: 140px; margin-left: 12px"/>
            <span class="muted note">两台都在 NAT 后时尝试 UDP 打洞，打通后不再经中转。需要协调节点。</span>
          </t-form-item>
          <t-form-item label="本机公网地址">
            <t-textarea v-model="form.public_addrs" :autosize="{minRows: 1, maxRows: 4}"
                        placeholder="可选，每行一个 host:port（端口映射 / DDNS 后的地址），对方据此公网直连"
                        style="max-width: 420px"/>
          </t-form-item>
          <t-form-item label="谁能使用远程控制">
            <t-radio-group v-model="form.control_role">
              <t-radio value="admin">仅管理员</t-radio>
              <t-radio value="operator">管理员与操作员</t-radio>
            </t-radio-group>
          </t-form-item>
          <t-form-item>
            <t-button theme="primary" @click="saveConfig" :loading="busy.config">保存</t-button>
          </t-form-item>
        </t-form>
      </section>

      <!-- ② 协调节点：只是一项配置（保存 / 替换），与上面的启动 / 停止无关 -->
      <section class="section">
        <h3>协调节点</h3>
        <template v-if="status.coordinator">
          <div class="kv">
            <span class="k">地址</span>
            <span class="v mono">{{ status.coordinator }}</span>
          </div>
          <div class="kv">
            <span class="k">证书指纹</span>
            <span class="v mono">{{ status.coordinator_id }}</span>
            <t-button v-if="status.coordinator_id" size="small" variant="text" @click="copy(status.coordinator_id)">复制
            </t-button>
          </div>
          <div class="kv">
            <span class="k">网络 ID</span>
            <span class="v mono">{{ status.network_id || '-' }}</span>
          </div>
          <div class="kv">
            <span class="k">连接</span>
            <span class="v" v-if="status.running">
              <t-tag size="small" :theme="status.connected ? 'success' : 'danger'" variant="light">
                {{ status.connected ? '已连接' : '未连接' }}
              </t-tag>
              <span v-if="status.connected && status.since" class="muted">自 {{ fmt(status.since) }}</span>
            </span>
            <span class="v muted" v-else>管理器互控未启动</span>
          </div>
          <div class="kv" v-if="status.running && status.observed_addr">
            <span class="k">出口地址</span>
            <span class="v mono">{{ status.observed_addr }}</span>
          </div>
          <div class="kv" v-if="status.running && status.stun_addrs?.length">
            <span class="k">STUN</span>
            <span class="v mono">{{ status.stun_addrs.join('，') }}</span>
          </div>
          <!-- 打洞（§12 P6）：NAT 类型由 STUN 判出，反射地址是对方打洞时用的公网地址 -->
          <div class="kv" v-if="status.running">
            <span class="k">打洞</span>
            <span class="v" v-if="status.punch?.active">
              <t-tag size="small" variant="light" :theme="NAT_TEXT[status.punch.mapping]?.[1] || 'default'"
                     :title="status.punch.error || ''">
                {{ NAT_TEXT[status.punch.mapping]?.[0] || status.punch.mapping }}
              </t-tag>
              <span class="muted">UDP {{ status.punch.udp_addr }}</span>
              <span v-if="status.punch.udp_error" class="err">{{ status.punch.udp_error }}</span>
            </span>
            <span class="v muted" v-else-if="status.no_punch">已关闭（本机设置里开启）</span>
            <span class="v err" v-else-if="status.punch?.error">不可用：{{ status.punch.error }}</span>
            <span class="v muted" v-else>不可用</span>
          </div>
          <div class="kv" v-if="status.running && status.punch?.srflx?.length">
            <span class="k">反射地址</span>
            <span class="v mono">{{ status.punch.srflx.join('，') }}</span>
          </div>
        </template>
        <div class="kv" v-else>
          <span class="v muted">未设置(只按对端的手填地址直连)</span>
        </div>

        <t-divider>{{ status.coordinator ? '替换协调节点' : '设置协调节点' }}</t-divider>
        <div class="row">
          <t-textarea :autosize="{
            minRows:3,
            maxRows:6
          }" v-model="joinBlob"
                      placeholder="粘贴 asa-mesh-join:v1:... 接入串（协调节点上 asa-coordinator join-blob 输出）"
                      clearable/>
          <t-button theme="primary" @click="saveCoordinator" :disabled="!preview" :loading="busy.join">
            {{ status.coordinator ? '替换' : '保存' }}
          </t-button>
        </div>
        <div v-if="previewError" class="err">{{ previewError }}</div>
        <div v-else-if="preview" class="preview">
          <div class="preview-title">解析结果</div>
          <div class="kv" v-for="row in previewRows" :key="row.k">
            <span class="k">{{ row.k }}</span>
            <span class="v" v-if="row.changed">
              <span class="mono muted strike">{{ row.cur }}</span>→<span class="mono changed">{{ row.next }}</span>
            </span>
            <span class="v" v-else>
              <span class="mono">{{ row.next || '-' }}</span>
              <span v-if="status.coordinator" class="muted">（未变）</span>
            </span>
          </div>
          <div class="kv">
            <span class="k">接入密钥</span>
            <span class="v" :class="{err: !preview.has_secret}">{{ preview.has_secret ? '已包含' : '缺失' }}</span>
          </div>
        </div>
      </section>

      <!-- ③ 我能控制的机器 -->
      <section class="section">
        <h3>我能控制的机器</h3>
        <div class="row">
          <t-textarea :autosize="{
            minRows:3,
            maxRows:6
          }" v-model="pairInvite" placeholder="粘贴对方生成的邀请码 asa-mesh-invite:v1:..." clearable/>
          <t-button theme="primary" @click="pairWithInvite" :disabled="!pairInvite.trim() || !status.running"
                    :loading="busy.pair">配对
          </t-button>
        </div>
        <div class="row">
          <div class="submit-connection">
            <t-input v-model="requestNode" placeholder="或输入对方的节点 ID 发起申请（对方管理员批准后生效）" clearable/>
            <t-input v-model="requestAddr" placeholder="可选：对方直连地址 host:port" clearable/>
          </div>
          <t-button variant="outline" @click="sendRequest" :disabled="!requestNode.trim() || !status.running"
                    :loading="busy.request">发起申请
          </t-button>
        </div>
        <t-table row-key="node_id" :data="outbound" :columns="outboundColumns" size="small" bordered
                 empty="还没有能控制的机器">
          <template #name="{row}">
            <div>{{ row.display_name }}</div>
            <div class="mono muted">{{ row.short_id }}</div>
          </template>
          <template #state="{row}">
            <t-tag v-if="row.last_error" theme="danger" variant="light" size="small" :title="row.last_error">离线
            </t-tag>
            <t-tag v-else-if="row.last_hello" theme="success" variant="light" size="small">在线</t-tag>
            <t-tag v-else size="small" variant="light">未检测</t-tag>
            <span v-if="row.last_hello" class="muted">
              {{ PATH_TEXT[row.last_hello.path] || row.last_hello.path }} · {{ row.last_hello.latency_ms }} ms
            </span>
            <span v-if="row.last_punch && !row.last_punch.ok" class="muted punch-fail"
                  :title="`${fmt(row.last_punch.at)}：${row.last_punch.reason}`">打洞未成功</span>
          </template>
          <template #version="{row}">{{ row.last_hello?.version || row.remote_version || '-' }}</template>
          <template #role="{row}">
            <span v-if="row.remote_role">{{ ROLE_TEXT[row.remote_role] }}</span>
            <span v-else class="muted">未授权 / 等待批准</span>
          </template>
          <template #actions="{row}">
            <t-space size="small">
              <t-button size="small" variant="text" @click="hello(row)" :loading="busy.hello === row.node_id">检测
              </t-button>
              <t-button size="small" variant="text" theme="primary" :disabled="!row.remote_role"
                        @click="switchToPeer(row)">切换过去
              </t-button>
              <t-button size="small" variant="text" @click="openEdit(row)">编辑</t-button>
              <t-popconfirm content="忘掉这台机器？也会同时撤销它对本机的授权。" @confirm="forget(row)">
                <t-button size="small" variant="text" theme="danger">删除</t-button>
              </t-popconfirm>
            </t-space>
          </template>
        </t-table>
      </section>

      <!-- ④ 能控制本机的机器 -->
      <section class="section">
        <h3>能控制本机的机器</h3>
        <t-table row-key="node_id" :data="inbound" :columns="inboundColumns" size="small" bordered
                 empty="还没有授权任何机器控制本机">
          <template #name="{row}">
            <div>{{ row.display_name }}</div>
            <div class="mono muted">{{ row.short_id }}</div>
          </template>
          <template #granted="{row}">
            <t-select :value="row.granted_role" size="small" style="width: 120px"
                      @change="v => setGrant(row, v)">
              <t-option value="operator" label="操作员"/>
              <t-option value="admin" label="管理员"/>
            </t-select>
          </template>
          <template #granted_at="{row}">{{ fmt(row.granted_at) }}</template>
          <template #actions="{row}">
            <t-popconfirm content="撤销后，对方正在进行的远程操作会立即断开。" @confirm="setGrant(row, '')">
              <t-button size="small" variant="text" theme="danger">撤销</t-button>
            </t-popconfirm>
          </template>
        </t-table>

        <h4 v-if="requests.length">待批准的申请</h4>
        <t-table v-if="requests.length" row-key="node_id" :data="requests" :columns="requestColumns" size="small"
                 bordered>
          <template #name="{row}">
            <div>{{ row.label || '（未填备注名）' }}</div>
            <div class="mono muted">{{ row.node_id }}</div>
          </template>
          <template #requested_at="{row}">{{ fmt(row.requested_at) }}</template>
          <template #actions="{row}">
            <t-space size="small">
              <t-select v-model="approveRole[row.node_id]" size="small" style="width: 110px" placeholder="角色">
                <t-option value="operator" label="操作员"/>
                <t-option value="admin" label="管理员"/>
              </t-select>
              <t-button size="small" theme="primary" @click="approve(row)">批准</t-button>
              <t-button size="small" variant="text" theme="danger" @click="reject(row)">拒绝</t-button>
            </t-space>
          </template>
        </t-table>
      </section>

      <!-- ⑤ 邀请码 -->
      <section class="section">
        <h3>邀请码</h3>
        <div class="row">
          <span>授予角色</span>
          <t-select v-model="inviteForm.role" style="width: 120px">
            <t-option value="operator" label="操作员"/>
            <t-option value="admin" label="管理员"/>
          </t-select>
          <span>有效期</span>
          <t-select v-model="inviteForm.ttl" style="width: 120px">
            <t-option :value="600" label="10 分钟"/>
            <t-option :value="3600" label="1 小时"/>
            <t-option :value="86400" label="24 小时"/>
          </t-select>
          <t-checkbox v-model="inviteForm.withLocalAddrs">附带本机直连地址</t-checkbox>
          <t-input v-model="inviteForm.note" placeholder="备注（对方配对后的名字）" style="max-width: 200px"/>
          <t-button theme="primary" @click="makeInvite" :loading="busy.invite">生成邀请码</t-button>
        </div>
        <t-table row-key="id" :data="invites" :columns="inviteColumns" size="small" bordered empty="没有未用的邀请码">
          <template #role="{row}">{{ ROLE_TEXT[row.role] }}</template>
          <template #expires_at="{row}">{{ fmt(row.expires_at) }}</template>
          <template #actions="{row}">
            <t-button size="small" variant="text" theme="danger" @click="dropInvite(row)">作废</t-button>
          </template>
        </t-table>
      </section>
    </div>

    <t-dialog v-model:visible="inviteDialog" header="邀请码（只显示这一次）" :footer="false" width="640px">
      <p class="muted">把整串发给要控制本机的那台机器，在它的「远程管理器 → 我能控制的机器」里粘贴。关闭后无法再次查看。</p>
      <t-textarea :value="newInvite" readonly :autosize="{minRows: 3}"/>
      <div class="dialog-actions">
        <t-button theme="primary" @click="copy(newInvite)">复制</t-button>
      </div>
    </t-dialog>

    <t-dialog v-model:visible="editDialog" header="编辑机器" @confirm="saveEdit" width="520px">
      <t-form label-width="90px">
        <t-form-item label="备注名">
          <t-input v-model="editForm.label"/>
        </t-form-item>
        <t-form-item label="直连地址">
          <t-textarea v-model="editForm.addrs" :autosize="{minRows: 2}"
                      placeholder="每行一个 host:port；没有协调节点时必填"/>
        </t-form-item>
      </t-form>
    </t-dialog>
  </t-card>
</template>

<script setup>
import {computed, onMounted, reactive, ref, watch} from 'vue'
import {DialogPlugin, MessagePlugin} from 'tdesign-vue-next'
import dayjs from 'dayjs'
import {authState} from '@/store/authStore.js'
import {switchToPeer} from '@/utils/peerContext.js'
import * as api from '@/apis/meshApi.js'

const PATH_TEXT = {lan: '内网直连', public: '公网直连', relay: '中转', punched: '打洞'}
const ROLE_TEXT = {admin: '管理员', operator: '操作员'}
// NAT 类型（status.punch.mapping）→ [文案, 标签主题]
const NAT_TEXT = {
  none: ['公网直达', 'success'],
  easy: ['易打洞', 'success'],
  hard: ['对称型，难打洞', 'warning'],
  unknown: ['NAT 类型未知', 'default'],
}

const loading = ref(false)
const busy = reactive({join: false, config: false, pair: false, request: false, invite: false, hello: '', life: ''})
const status = ref({})
const peers = ref([])
const requests = ref([])
const invites = ref([])
const joinBlob = ref('')
const preview = ref(null)
const previewError = ref('')
const pairInvite = ref('')
const requestNode = ref('')
const requestAddr = ref('')
const approveRole = reactive({})
const inviteForm = reactive({role: 'operator', ttl: 600, withLocalAddrs: true, note: ''})
const inviteDialog = ref(false)
const newInvite = ref('')
const editDialog = ref(false)
const editForm = reactive({node_id: '', label: '', addrs: ''})
const form = reactive({
  label: '', listen: true, peer_port: 19194, public_addrs: '', control_role: 'admin', punch: true, udp_port: undefined,
})

// 粘贴的 join blob 与当前已保存的协调节点逐项对照（已保存时才标「变化」）
const previewRows = computed(() => {
  const p = preview.value
  if (!p) return []
  const has = !!status.value.coordinator
  return [
    {k: '地址', cur: status.value.coordinator, next: p.addr},
    {k: '证书指纹', cur: status.value.coordinator_id, next: p.coordinator_id},
    {k: '网络 ID', cur: status.value.network_id, next: p.network_id},
  ].map(r => ({...r, changed: has && r.cur !== r.next}))
})

// 出站：对方授予了本机角色，或本机发起过配对 / 手填过地址（还没拿到授权）
const outbound = computed(() => peers.value.filter(p => p.remote_role || !p.granted_role))
// 入站：本机授予了对方角色
const inbound = computed(() => peers.value.filter(p => p.granted_role))

const outboundColumns = [
  {colKey: 'name', title: '机器', width: 200},
  {colKey: 'state', title: '状态'},
  {colKey: 'version', title: '版本', width: 110},
  {colKey: 'role', title: '对方授予本机', width: 140},
  {colKey: 'actions', title: '操作', width: 280},
]
const inboundColumns = [
  {colKey: 'name', title: '机器', width: 220},
  {colKey: 'granted', title: '授予的角色', width: 150},
  {colKey: 'granted_at', title: '授权时间', width: 170},
  {colKey: 'actions', title: '操作', width: 100},
]
const requestColumns = [
  {colKey: 'name', title: '申请者'},
  {colKey: 'version', title: '版本', width: 100},
  {colKey: 'addr', title: '来源', width: 180},
  {colKey: 'requested_at', title: '时间', width: 170},
  {colKey: 'actions', title: '操作', width: 300},
]
const inviteColumns = [
  {colKey: 'id', title: '编号', width: 140},
  {colKey: 'role', title: '角色', width: 100},
  {colKey: 'note', title: '备注'},
  {colKey: 'expires_at', title: '到期', width: 170},
  {colKey: 'actions', title: '操作', width: 80},
]

const fmt = t => (t ? dayjs(t).format('YYYY-MM-DD HH:mm:ss') : '-')
const lines = s => s.split(/\r?\n/).map(x => x.trim()).filter(Boolean)
const fail = e => MessagePlugin.error(e?.message || String(e))

async function copy(text) {
  try {
    await navigator.clipboard.writeText(text)
    MessagePlugin.success('已复制')
  } catch (e) {
    MessagePlugin.warning('浏览器不允许写剪贴板，请手动选中复制')
  }
}

function applyStatus(st) {
  status.value = st || {}
  form.label = st?.label || ''
  form.listen = !st?.no_listen
  form.peer_port = st?.peer_port || 19194
  form.public_addrs = (st?.public_addrs || []).join('\n')
  form.control_role = st?.control_role || 'admin'
  form.punch = !st?.no_punch
  form.udp_port = st?.udp_port || undefined
}

async function reloadAll() {
  loading.value = true
  try {
    const [st, ps, rq, iv] = await Promise.all([api.getMeshStatus(), api.listPeers(), api.listRequests(), api.listInvites()])
    applyStatus(st)
    peers.value = ps || []
    requests.value = rq || []
    invites.value = iv || []
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}

async function run(key, fn, okMsg) {
  busy[key] = true
  try {
    const res = await fn()
    if (okMsg) MessagePlugin.success(okMsg)
    return res
  } catch (e) {
    fail(e)
  } finally {
    busy[key] = false
  }
}

// 保存类接口的响应带 applied：mesh 在运行就热应用，停止时只保存、下次启动生效（不会因为改配置把它拉起来）
function savedMessage(st) {
  MessagePlugin.success(st.applied ? '已保存并应用' : '已保存，启动后生效')
}

// 粘贴后防抖解析：只预览，不保存。序号丢弃过期的响应（连续粘贴 / 编辑时后发先至）
let previewSeq = 0
let previewTimer = null
watch(joinBlob, v => {
  clearTimeout(previewTimer)
  const blob = v.trim()
  const seq = ++previewSeq
  preview.value = null
  previewError.value = ''
  if (!blob) return
  previewTimer = setTimeout(async () => {
    try {
      const p = await api.previewJoinBlob(blob)
      if (seq === previewSeq) preview.value = p
    } catch (e) {
      if (seq === previewSeq) previewError.value = e?.message || String(e)
    }
  }, 300)
})

async function saveCoordinator() {
  const doSave = async () => {
    const st = await run('join', () => api.joinCoordinator(joinBlob.value.trim()))
    if (st) {
      joinBlob.value = ''
      applyStatus(st)
      savedMessage(st)
    }
  }
  if (!status.value.coordinator) return doSave()
  const dlg = DialogPlugin.confirm({
    header: '替换协调节点',
    body: `替换后本机将改为登记到新的协调节点（${preview.value?.addr}）。经旧协调节点中转的连接会断开。`,
    confirmBtn: '替换',
    cancelBtn: '取消',
    onConfirm: () => {
      dlg.destroy()
      doSave()
    },
  })
}

// 顶部的启动 / 停止 / 重启。启动与停止会持久化启用开关（服务重启后保持）
const LIFECYCLE = {
  start: [api.enableMesh, '已启动'],
  stop: [api.disableMesh, '已停止'],
  restart: [api.restartMesh, '已重启'],
}

async function lifecycle(action) {
  const [fn, okMsg] = LIFECYCLE[action]
  busy.life = action
  try {
    applyStatus(await fn())
    MessagePlugin.success(okMsg)
  } catch (e) {
    fail(e)
    // 启动失败时状态里带着原因（顶部标签变「启动失败」）
    applyStatus(await api.getMeshStatus().catch(() => status.value))
  } finally {
    busy.life = ''
  }
  const ps = await api.listPeers().catch(() => null)
  if (ps) peers.value = ps
}

async function saveConfig() {
  const st = await run('config', () => api.updateMeshConfig({
    label: form.label,
    no_listen: !form.listen,
    peer_port: form.peer_port,
    public_addrs: lines(form.public_addrs),
    control_role: form.control_role,
    no_punch: !form.punch,
    udp_port: form.udp_port || 0,
  }))
  if (st) {
    applyStatus(st)
    savedMessage(st)
  }
}

async function pairWithInvite() {
  const res = await run('pair', () => api.pairPeer({invite: pairInvite.value.trim()}))
  if (res) {
    pairInvite.value = ''
    MessagePlugin.success(`已配对：${res.label || res.node_id.slice(0, 8)}，对方授予本机${ROLE_TEXT[res.granted_role] || res.granted_role}`)
    reloadAll()
  }
}

async function sendRequest() {
  const body = {node_id: requestNode.value.trim()}
  if (requestAddr.value.trim()) body.addrs = [requestAddr.value.trim()]
  const res = await run('request', () => api.pairPeer(body))
  if (res) {
    requestNode.value = ''
    requestAddr.value = ''
    MessagePlugin.success(res.status === 'paired' ? '对方已授权本机' : '已提交申请，等待对方管理员批准（之后点「检测」刷新）')
    reloadAll()
  }
}

async function hello(row) {
  busy.hello = row.node_id
  try {
    await api.helloPeer(row.node_id)
  } catch (e) {
    fail(e)
  } finally {
    busy.hello = ''
    const ps = await api.listPeers().catch(() => null)
    if (ps) peers.value = ps
  }
}

function openEdit(row) {
  editForm.node_id = row.node_id
  editForm.label = row.label || ''
  editForm.addrs = (row.addrs || []).join('\n')
  editDialog.value = true
}

async function saveEdit() {
  try {
    await api.updatePeer(editForm.node_id, {label: editForm.label, addrs: lines(editForm.addrs)})
    editDialog.value = false
    MessagePlugin.success('已保存')
    reloadAll()
  } catch (e) {
    fail(e)
  }
}

async function forget(row) {
  try {
    await api.forgetPeer(row.node_id)
    reloadAll()
  } catch (e) {
    fail(e)
  }
}

async function setGrant(row, role) {
  try {
    await api.updatePeer(row.node_id, {granted_role: role})
    MessagePlugin.success(role ? '已修改授权' : '已撤销授权')
    reloadAll()
  } catch (e) {
    fail(e)
  }
}

async function approve(row) {
  const role = approveRole[row.node_id]
  if (!role) {
    MessagePlugin.warning('先选择要授予的角色')
    return
  }
  try {
    await api.approveRequest(row.node_id, role)
    MessagePlugin.success('已批准')
    reloadAll()
  } catch (e) {
    fail(e)
  }
}

async function reject(row) {
  try {
    await api.rejectRequest(row.node_id)
    reloadAll()
  } catch (e) {
    fail(e)
  }
}

async function makeInvite() {
  const res = await run('invite', () => api.createInvite({
    role: inviteForm.role,
    ttl_seconds: inviteForm.ttl,
    with_local_addrs: inviteForm.withLocalAddrs,
    note: inviteForm.note,
  }))
  if (res?.invite) {
    newInvite.value = res.invite
    inviteDialog.value = true
    inviteForm.note = ''
    invites.value = (await api.listInvites().catch(() => null)) || invites.value
  }
}

async function dropInvite(row) {
  try {
    await api.deleteInvite(row.id)
    invites.value = invites.value.filter(v => v.id !== row.id)
  } catch (e) {
    fail(e)
  }
}

onMounted(reloadAll)
</script>

<style scoped lang="less">
.mesh-card {
  height: 100%;
  overflow: auto;
}

.mesh-header {
  display: flex;
  align-items: center;
  gap: 10px;

  .page-title {
    font-size: 16px;
    font-weight: 600;
  }

  .spacer {
    flex: 1;
  }
}

.mesh-body {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.block-alert {
  margin-bottom: 4px;
}

.section {
  background: #fafafa;
  border-radius: 8px;
  padding: 12px 16px;

  .submit-connection {
    display: flex;
    align-items: center;
    flex-wrap: nowrap;
    width: 100%;
    gap: 10px;
  }

  h3 {
    margin: 0 0 10px;
    font-size: 15px;
  }

  h4 {
    margin: 16px 0 8px;
    font-size: 14px;
  }
}

.kv {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 30px;

  .k {
    width: 90px;
    color: #666;
    flex: 0 0 auto;
  }

  .v {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
}

.row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
  flex-wrap: wrap;
}

.mono {
  font-family: Consolas, Menlo, monospace;
  word-break: break-all;
}

.muted {
  color: #888;
  font-size: 12px;
}

.note {
  margin-left: 12px;
}

.err {
  color: #d54941;
}

.punch-fail {
  text-decoration: underline dotted;
  cursor: help;
}

.cfg-form {
  max-width: 900px;
}

.preview {
  margin-top: 4px;
  padding: 8px 12px;
  border: 1px dashed #d0d0d0;
  border-radius: 6px;
  background: #fff;

  .preview-title {
    font-size: 13px;
    color: #666;
    margin-bottom: 4px;
  }

  .strike {
    text-decoration: line-through;
  }

  .changed {
    color: #0052d9;
  }
}

.dialog-actions {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
}
</style>
