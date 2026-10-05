<template>
  <!-- 远程管理器（管理器互控，docs/REMOTE_MANAGER_MESH_PLAN.md §7.3、§12 P4-4）。只在本机上下文出现。 -->
  <t-card class="mesh-card layout-card" :bordered="false">
    <template #title>
      <div class="mesh-header">
        <span class="page-title">远程管理器</span>
        <t-tag v-if="status.running" theme="success" variant="light">运行中</t-tag>
        <t-tag v-else-if="status.configured" theme="warning" variant="light">已配置，未运行</t-tag>
        <t-tag v-else variant="light">未启用</t-tag>
        <span class="spacer"/>
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
          <span class="v mono">{{ status.node_id || '尚未生成（接入或启用时生成）' }}</span>
          <t-button v-if="status.node_id" size="small" variant="text" @click="copy(status.node_id)">复制</t-button>
        </div>
        <div class="kv">
          <span class="k">协调节点</span>
          <span class="v" v-if="status.coordinator">
            {{ status.coordinator }}
            <t-tag size="small" :theme="status.connected ? 'success' : 'danger'" variant="light">
              {{ status.connected ? '已连接' : '未连接' }}
            </t-tag>
            <span v-if="status.observed_addr" class="muted">出口地址 {{ status.observed_addr }}</span>
          </span>
          <span class="v muted" v-else>未接入（{{ status.enabled ? '无协调节点模式：只按手填地址直连' : '未启用' }}）</span>
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

        <t-divider>接入协调节点</t-divider>
        <div class="row">
          <t-input v-model="joinBlob" placeholder="粘贴 asa-mesh-join:v1:... 接入串（协调节点上 asa-coordinator join-blob 输出）"
                   clearable/>
          <t-button theme="primary" @click="doJoin" :disabled="!joinBlob.trim()" :loading="busy.join">接入</t-button>
          <t-popconfirm v-if="status.coordinator" content="断开协调节点并停用管理器互控？本机身份与配对关系会保留。"
                        @confirm="doLeave">
            <t-button theme="danger" variant="outline">断开接入</t-button>
          </t-popconfirm>
          <t-button v-if="!status.enabled" variant="outline" @click="doEnable(true)">启用（无协调节点）</t-button>
          <t-button v-else-if="!status.coordinator" variant="outline" @click="doEnable(false)">停用</t-button>
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
            <t-button theme="primary" @click="saveConfig" :loading="busy.config">保存并应用</t-button>
          </t-form-item>
        </t-form>
      </section>

      <!-- ② 我能控制的机器 -->
      <section class="section">
        <h3>我能控制的机器</h3>
        <div class="row">
          <t-input v-model="pairInvite" placeholder="粘贴对方生成的邀请码 asa-mesh-invite:v1:..." clearable/>
          <t-button theme="primary" @click="pairWithInvite" :disabled="!pairInvite.trim() || !status.running"
                    :loading="busy.pair">配对
          </t-button>
        </div>
        <div class="row">
          <t-input v-model="requestNode" placeholder="或输入对方的节点 ID 发起申请（对方管理员批准后生效）" clearable/>
          <t-input v-model="requestAddr" placeholder="可选：对方直连地址 host:port" style="max-width: 260px" clearable/>
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
            <t-tag v-if="row.last_error" theme="danger" variant="light" size="small" :title="row.last_error">离线</t-tag>
            <t-tag v-else-if="row.last_hello" theme="success" variant="light" size="small">在线</t-tag>
            <t-tag v-else size="small" variant="light">未检测</t-tag>
            <span v-if="row.last_hello" class="muted">
              {{ PATH_TEXT[row.last_hello.path] || row.last_hello.path }} · {{ row.last_hello.latency_ms }} ms
            </span>
          </template>
          <template #version="{row}">{{ row.last_hello?.version || row.remote_version || '-' }}</template>
          <template #role="{row}">
            <span v-if="row.remote_role">{{ ROLE_TEXT[row.remote_role] }}</span>
            <span v-else class="muted">未授权 / 等待批准</span>
          </template>
          <template #actions="{row}">
            <t-space size="small">
              <t-button size="small" variant="text" @click="hello(row)" :loading="busy.hello === row.node_id">检测</t-button>
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

      <!-- ③ 能控制本机的机器 -->
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

      <!-- ④ 邀请码 -->
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
          <t-textarea v-model="editForm.addrs" :autosize="{minRows: 2}" placeholder="每行一个 host:port；没有协调节点时必填"/>
        </t-form-item>
      </t-form>
    </t-dialog>
  </t-card>
</template>

<script setup>
import {computed, onMounted, reactive, ref} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'
import dayjs from 'dayjs'
import {authState} from '@/store/authStore.js'
import {switchToPeer} from '@/utils/peerContext.js'
import * as api from '@/apis/meshApi.js'

const PATH_TEXT = {lan: '内网直连', public: '公网直连', relay: '中转', punched: '打洞'}
const ROLE_TEXT = {admin: '管理员', operator: '操作员'}

const loading = ref(false)
const busy = reactive({join: false, config: false, pair: false, request: false, invite: false, hello: ''})
const status = ref({})
const peers = ref([])
const requests = ref([])
const invites = ref([])
const joinBlob = ref('')
const pairInvite = ref('')
const requestNode = ref('')
const requestAddr = ref('')
const approveRole = reactive({})
const inviteForm = reactive({role: 'operator', ttl: 600, withLocalAddrs: true, note: ''})
const inviteDialog = ref(false)
const newInvite = ref('')
const editDialog = ref(false)
const editForm = reactive({node_id: '', label: '', addrs: ''})
const form = reactive({label: '', listen: true, peer_port: 19194, public_addrs: '', control_role: 'admin'})

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

async function doJoin() {
  const st = await run('join', () => api.joinCoordinator(joinBlob.value.trim()), '已接入')
  if (st) {
    joinBlob.value = ''
    applyStatus(st)
  }
}

async function doLeave() {
  applyStatus(await run('join', api.leaveCoordinator, '已断开接入'))
}

async function doEnable(on) {
  applyStatus(await run('join', on ? api.enableMesh : api.disableMesh, on ? '已启用' : '已停用'))
}

async function saveConfig() {
  const st = await run('config', () => api.updateMeshConfig({
    label: form.label,
    no_listen: !form.listen,
    peer_port: form.peer_port,
    public_addrs: lines(form.public_addrs),
    control_role: form.control_role,
  }), '已保存并应用')
  if (st) applyStatus(st)
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

.cfg-form {
  max-width: 900px;
}

.dialog-actions {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
}
</style>
