<template>
  <div class="user-manager">
    <t-card title="用户管理" :bordered="false">
      <template #actions>
        <t-space>
          <t-button variant="outline" @click="loadAudit">审计日志</t-button>
          <t-button theme="primary" @click="openCreate">新增账户</t-button>
        </t-space>
      </template>

      <t-table
          row-key="username"
          :data="users"
          :columns="columns"
          :loading="loading"
          size="medium"
          stripe
      />
    </t-card>

    <!-- 新增账户 -->
    <t-dialog
        v-model:visible="createVisible"
        header="新增账户"
        :confirm-btn="{content: '创建', loading: submitting}"
        @confirm="onCreate"
    >
      <t-form label-width="80px">
        <t-form-item label="用户名">
          <t-input v-model="form.username" placeholder="3-32 位字母、数字、下划线或连字符"/>
        </t-form-item>
        <t-form-item label="密码">
          <t-input v-model="form.password" type="password" placeholder="至少 8 位"/>
        </t-form-item>
        <t-form-item label="角色">
          <t-select v-model="form.role">
            <t-option value="operator" label="操作员（可操作服务器，不能管用户）"/>
            <t-option value="admin" label="管理员（可管理用户）"/>
          </t-select>
        </t-form-item>
      </t-form>
      <t-alert theme="info" message="密码是必填项，它是唯一的登录方式。"/>
    </t-dialog>

    <!-- 重置密码 -->
    <t-dialog
        v-model:visible="pwdVisible"
        :header="`重置 ${target?.username} 的密码`"
        :confirm-btn="{content: '重置', loading: submitting}"
        @confirm="onResetPassword"
    >
      <t-input v-model="newPassword" type="password" placeholder="新密码"/>
      <t-alert theme="warning" style="margin-top:12px"
               message="重置后该用户的所有已登录设备都会被登出。"/>
    </t-dialog>

    <!-- 审计日志 -->
    <t-drawer v-model:visible="auditVisible" header="审计日志" size="720px" :footer="false">
      <t-space direction="vertical" style="width:100%">
        <t-space>
          <t-input v-model="auditFilter.user" placeholder="按用户名筛选" style="width:180px" clearable/>
          <t-select v-model="auditFilter.event" placeholder="按事件筛选" style="width:200px" clearable>
            <t-option v-for="(label, key) in eventLabels" :key="key" :value="key" :label="label"/>
          </t-select>
          <t-button variant="outline" @click="loadAudit">刷新</t-button>
        </t-space>
        <t-table
            row-key="id"
            :data="auditEntries"
            :columns="auditColumns"
            :loading="auditLoading"
            size="small"
            max-height="calc(100vh - 200px)"
        />
      </t-space>
    </t-drawer>
  </div>
</template>

<script setup>
import {computed, h, onMounted, reactive, ref} from 'vue'
import {MessagePlugin, DialogPlugin, Button, Tag, Space} from 'tdesign-vue-next'
import * as authApi from '@/apis/authApi.js'
import {authState} from '@/store/authStore.js'

const users = ref([])
const loading = ref(false)
const submitting = ref(false)

const createVisible = ref(false)
const pwdVisible = ref(false)
const target = ref(null)
const newPassword = ref('')
const form = reactive({username: '', password: '', role: 'operator'})

const auditVisible = ref(false)
const auditLoading = ref(false)
const auditEntries = ref([])
const auditFilter = reactive({user: '', event: ''})

const eventLabels = {
  login_ok: '登录成功',
  login_fail: '登录失败',
  totp_fail: '两步验证失败',
  logout: '登出',
  logout_all: '登出全部设备',
  passwd_change: '修改密码',
  passwd_reset: '重置密码',
  user_create: '创建账户',
  user_delete: '删除账户',
  user_update: '修改账户',
  user_unlock: '解除锁定',
  totp_bind: '绑定两步验证',
  totp_reset: '解绑两步验证',
  // Passkey 功能已移除，但历史审计记录里仍有这两种 action，
  // 保留标签是为了让旧记录仍能显示成中文而不是裸的 action 名。
  cred_add: '添加 Passkey',
  cred_delete: '删除 Passkey',
}

const currentUsername = computed(() => authState.user?.username || '')

const columns = [
  {colKey: 'username', title: '用户名', width: 160},
  {
    colKey: 'role', title: '角色', width: 100,
    cell: (_, {row}) => h(Tag, {
      theme: row.role === 'admin' ? 'primary' : 'default',
      variant: 'light',
    }, () => row.role === 'admin' ? '管理员' : '操作员'),
  },
  {
    colKey: 'disabled', title: '状态', width: 90,
    cell: (_, {row}) => h(Tag, {
      theme: row.disabled ? 'danger' : 'success', variant: 'light',
    }, () => row.disabled ? '已禁用' : '启用'),
  },
  {
    colKey: 'totp_enabled', title: '两步验证', width: 100,
    cell: (_, {row}) => row.totp_enabled ? '已绑定' : '未绑定',
  },
  {
    colKey: 'last_login_at', title: '最后登录', width: 170,
    cell: (_, {row}) => row.last_login_at ? fmt(row.last_login_at) : '从未',
  },
  {
    colKey: 'ops', title: '操作', minWidth: 320,
    cell: (_, {row}) => h(Space, {size: 'small'}, () => [
      h(Button, {size: 'small', variant: 'text', onClick: () => openResetPassword(row)}, () => '重置密码'),
      row.totp_enabled
          ? h(Button, {size: 'small', variant: 'text', onClick: () => resetTOTP(row)}, () => '解绑 2FA')
          : null,
      h(Button, {size: 'small', variant: 'text', onClick: () => unlock(row)}, () => '解除锁定'),
      h(Button, {
        size: 'small', variant: 'text',
        onClick: () => toggleRole(row),
      }, () => row.role === 'admin' ? '降为操作员' : '升为管理员'),
      // 禁用和删除自己都会立刻把自己踢下线，几乎肯定是误操作，直接不给按钮
      row.username !== currentUsername.value
          ? h(Button, {
            size: 'small', variant: 'text',
            onClick: () => toggleDisabled(row),
          }, () => row.disabled ? '启用' : '禁用')
          : null,
      row.username !== currentUsername.value
          ? h(Button, {
            size: 'small', variant: 'text', theme: 'danger',
            onClick: () => removeUser(row),
          }, () => '删除')
          : null,
    ]),
  },
]

const auditColumns = [
  {colKey: 'timestamp', title: '时间', width: 160, cell: (_, {row}) => fmt(row.timestamp)},
  {colKey: 'event', title: '事件', width: 130, cell: (_, {row}) => eventLabels[row.event] || row.event},
  {colKey: 'username', title: '对象', width: 110},
  {colKey: 'actor', title: '操作者', width: 110},
  {colKey: 'client_ip', title: '来源 IP', width: 130},
  {colKey: 'detail', title: '详情', minWidth: 140},
]

function fmt(s) {
  if (!s) return '-'
  const d = new Date(s)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

async function load() {
  loading.value = true
  try {
    const res = await authApi.listUsers()
    users.value = res.users || []
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.username = ''
  form.password = ''
  form.role = 'operator'
  createVisible.value = true
}

async function onCreate() {
  submitting.value = true
  try {
    await authApi.createUser(form.username, form.password, form.role)
    MessagePlugin.success('账户已创建')
    createVisible.value = false
    await load()
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    submitting.value = false
  }
}

function openResetPassword(row) {
  target.value = row
  newPassword.value = ''
  pwdVisible.value = true
}

async function onResetPassword() {
  submitting.value = true
  try {
    await authApi.resetUserPassword(target.value.username, newPassword.value)
    MessagePlugin.success('密码已重置，该用户所有设备已登出')
    pwdVisible.value = false
    await load()
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    submitting.value = false
  }
}

function confirmThen(header, body, fn) {
  const d = DialogPlugin.confirm({
    header,
    body,
    onConfirm: async () => {
      try {
        await fn()
        await load()
      } catch (e) {
        MessagePlugin.error(e.message)
      }
      d.hide()
    },
  })
}

function resetTOTP(row) {
  confirmThen('解绑两步验证',
      `确认解绑 ${row.username} 的两步验证？其恢复码也会被一并清除。用户丢失手机时用这个救援。`,
      async () => {
        await authApi.resetUserTOTP(row.username)
        MessagePlugin.success('已解绑')
      })
}

async function unlock(row) {
  try {
    await authApi.unlockUser(row.username)
    MessagePlugin.success('已解除登录锁定')
  } catch (e) {
    MessagePlugin.error(e.message)
  }
}

function toggleRole(row) {
  const next = row.role === 'admin' ? 'operator' : 'admin'
  confirmThen('修改角色', `确认把 ${row.username} 改为${next === 'admin' ? '管理员' : '操作员'}？`,
      async () => {
        await authApi.updateUser(row.username, {role: next})
        MessagePlugin.success('已修改')
      })
}

function toggleDisabled(row) {
  const next = !row.disabled
  confirmThen(next ? '禁用账户' : '启用账户',
      next ? `禁用 ${row.username}？其所有会话会立刻失效。` : `启用 ${row.username}？`,
      async () => {
        await authApi.updateUser(row.username, {disabled: next})
        MessagePlugin.success('已更新')
      })
}

function removeUser(row) {
  confirmThen('删除账户', `确认删除 ${row.username}？此操作不可撤销。`,
      async () => {
        await authApi.deleteUser(row.username)
        MessagePlugin.success('已删除')
      })
}

async function loadAudit() {
  auditVisible.value = true
  auditLoading.value = true
  try {
    const res = await authApi.getAuditLog({
      user: auditFilter.user || undefined,
      event: auditFilter.event || undefined,
      limit: 200,
    })
    auditEntries.value = res.entries || []
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    auditLoading.value = false
  }
}

onMounted(load)
</script>

<style scoped lang="less">
.user-manager {
  padding: 16px;
}
</style>
