<template>
  <div class="profile">
    <t-card title="账户信息" :bordered="false" class="profile-card">
      <t-descriptions :column="2" bordered>
        <t-descriptions-item label="用户名">{{ authState.user?.username || '-' }}</t-descriptions-item>
        <t-descriptions-item label="角色">
          {{ authState.user?.role === 'admin' ? '管理员' : '操作员' }}
        </t-descriptions-item>
      </t-descriptions>
    </t-card>

    <t-card title="修改密码" :bordered="false" class="profile-card">
      <t-alert theme="info" message="修改密码后，其他设备上的登录会立刻失效（当前设备会自动续期）。"
               style="margin-bottom: var(--td-comp-paddingTB-l)"
      />
      <t-form label-width="90px" style="max-width:420px">
        <t-form-item label="当前密码">
          <t-input v-model="pwd.old" type="password" autocomplete="current-password"/>
        </t-form-item>
        <t-form-item label="新密码">
          <t-input v-model="pwd.new1" type="password" autocomplete="new-password"/>
        </t-form-item>
        <t-form-item label="确认新密码">
          <t-input v-model="pwd.new2" type="password" autocomplete="new-password"/>
        </t-form-item>
        <t-form-item>
          <t-button theme="primary" :loading="pwdLoading" @click="onChangePassword">保存</t-button>
        </t-form-item>
      </t-form>
    </t-card>

    <t-card title="两步验证" :bordered="false" class="profile-card">
      <template v-if="authState.user?.totp_enabled">
        <t-space direction="vertical" style="width:100%">
          <t-tag theme="success" variant="light">已启用</t-tag>
          <t-space>
            <t-button variant="outline" @click="regenVisible = true">重新生成恢复码</t-button>
            <t-button theme="danger" variant="outline" @click="disableVisible = true">解绑</t-button>
          </t-space>
        </t-space>
      </template>
      <template v-else>
        <p class="hint">绑定后，登录时除密码外还需要输入验证器 App 中的 6 位验证码。</p>
        <t-button theme="primary" :loading="totpLoading" @click="startBindTOTP">开始绑定</t-button>
      </template>
    </t-card>

    <t-card title="会话" :bordered="false" class="profile-card">
      <t-button theme="danger" variant="outline" @click="onLogoutAll">登出所有设备</t-button>
    </t-card>

    <!-- 绑定两步验证 -->
    <t-dialog v-model:visible="bindVisible" header="绑定两步验证" :footer="false" width="420px">
      <div class="totp-bind">
        <p class="hint">用验证器 App 扫描下方二维码，然后输入它显示的 6 位验证码。</p>
        <img v-if="totpSetupData.qr" class="qr" :src="`data:image/png;base64,${totpSetupData.qr}`" alt="二维码">
        <p class="secret">无法扫码？手动输入密钥：<code>{{ totpSetupData.secret }}</code></p>
        <t-input v-model="totpCode" placeholder="6 位验证码" size="large" @enter="confirmBindTOTP"/>
        <t-button theme="primary" block style="margin-top:12px" :loading="totpLoading" @click="confirmBindTOTP">
          确认绑定
        </t-button>
      </div>
    </t-dialog>

    <!-- 恢复码展示：明文只出现这一次 -->
    <t-dialog v-model:visible="codesVisible" header="请保存恢复码" :cancel-btn="null" @confirm="codesVisible = false"
              confirm-btn="我已保存">
      <t-alert theme="warning" message="这些恢复码不会再次显示。手机丢失时用它们登录，每个只能用一次。"/>
      <div class="codes">
        <code v-for="c in recoveryCodes" :key="c">{{ c }}</code>
      </div>
      <t-button variant="outline" size="small" @click="copyCodes">复制全部</t-button>
    </t-dialog>

    <!-- 解绑 -->
    <t-dialog v-model:visible="disableVisible" header="解绑两步验证"
              :confirm-btn="{content:'解绑', theme:'danger', loading: totpLoading}" @confirm="onDisableTOTP">
      <t-form label-width="90px">
        <t-form-item label="当前密码">
          <t-input v-model="disableForm.password" type="password"/>
        </t-form-item>
        <t-form-item label="验证码">
          <t-input v-model="disableForm.code" placeholder="6 位验证码"/>
        </t-form-item>
      </t-form>
    </t-dialog>

    <!-- 重新生成恢复码 -->
    <t-dialog v-model:visible="regenVisible" header="重新生成恢复码"
              :confirm-btn="{content:'生成', loading: totpLoading}" @confirm="onRegenCodes">
      <p>生成后，旧的恢复码将<strong>全部作废</strong>。</p>
    </t-dialog>
  </div>
</template>

<script setup>
import {computed, onMounted, reactive, ref} from 'vue'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'
import * as authApi from '@/apis/authApi.js'
import {authState, recheck} from '@/store/authStore.js'

const pwd = reactive({old: '', new1: '', new2: ''})
const pwdLoading = ref(false)

const totpLoading = ref(false)
const bindVisible = ref(false)
const codesVisible = ref(false)
const disableVisible = ref(false)
const regenVisible = ref(false)
const totpCode = ref('')
const totpSetupData = reactive({secret: '', qr: ''})
const recoveryCodes = ref([])
const disableForm = reactive({password: '', code: ''})

function fmt(s) {
  if (!s) return '-'
  const d = new Date(s)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

async function onChangePassword() {
  if (pwd.new1 !== pwd.new2) {
    MessagePlugin.error('两次输入的新密码不一致')
    return
  }
  pwdLoading.value = true
  try {
    await authApi.changePassword(pwd.old, pwd.new1)
    MessagePlugin.success('密码已修改，其他设备已登出')
    pwd.old = pwd.new1 = pwd.new2 = ''
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    pwdLoading.value = false
  }
}

async function startBindTOTP() {
  totpLoading.value = true
  try {
    const res = await authApi.totpSetup()
    totpSetupData.secret = res.secret
    totpSetupData.qr = res.qr_png_base64
    totpCode.value = ''
    bindVisible.value = true
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    totpLoading.value = false
  }
}

async function confirmBindTOTP() {
  totpLoading.value = true
  try {
    const res = await authApi.totpConfirm(totpCode.value)
    recoveryCodes.value = res.recovery_codes || []
    bindVisible.value = false
    codesVisible.value = true
    await recheck(true)
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    totpLoading.value = false
  }
}

async function onDisableTOTP() {
  totpLoading.value = true
  try {
    await authApi.totpDisable(disableForm.password, disableForm.code)
    MessagePlugin.success('已解绑')
    disableVisible.value = false
    disableForm.password = disableForm.code = ''
    await recheck(true)
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    totpLoading.value = false
  }
}

async function onRegenCodes() {
  totpLoading.value = true
  try {
    const res = await authApi.regenerateRecoveryCodes()
    recoveryCodes.value = res.recovery_codes || []
    regenVisible.value = false
    codesVisible.value = true
  } catch (e) {
    MessagePlugin.error(e.message)
  } finally {
    totpLoading.value = false
  }
}

function copyCodes() {
  navigator.clipboard?.writeText(recoveryCodes.value.join('\n'))
      .then(() => MessagePlugin.success('已复制'))
      .catch(() => MessagePlugin.error('复制失败，请手动选择文本'))
}

</script>

<style scoped lang="less">
.profile {
  padding: 16px;
  max-width: 760px;

  :deep(.t-card__body) {
    padding: 0 var(--td-comp-paddingLR-xl) var(--td-comp-paddingTB-l) var(--td-comp-paddingLR-xl);
  }
}

.profile-card {
  margin-bottom: 16px;
}

.hint {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.sub {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.totp-bind {
  text-align: center;

  .qr {
    width: 200px;
    height: 200px;
    margin: 8px 0;
  }

  .secret {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    margin-bottom: 12px;

    code {
      user-select: all;
      padding: 2px 6px;
      border-radius: 3px;
      background: var(--td-bg-color-secondarycontainer);
    }
  }
}

.codes {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 8px;
  margin: 16px 0;

  code {
    padding: 6px 10px;
    border-radius: 4px;
    background: var(--td-bg-color-secondarycontainer);
    font-family: monospace;
    letter-spacing: 1px;
    user-select: all;
  }
}
</style>
