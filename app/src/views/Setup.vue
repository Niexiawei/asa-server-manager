<template>
  <div class="auth-page">
    <div class="auth-card">
      <div class="auth-logo">
        <img src="/ASA_Logo_transparent.webp" alt="ASA">
        <h2>创建管理员账号</h2>
      </div>

      <t-alert
          v-if="!authState.setupAllowed"
          theme="warning"
          class="auth-error"
      >
        <template #message>
          出于安全考虑，第一个管理员账号只能在服务器本机创建。<br>
          请在服务器上打开浏览器访问本页面，或在服务器上执行：<br>
          <code>asa-server.exe user add &lt;用户名&gt; --role admin</code>
        </template>
      </t-alert>

      <template v-else>
        <p class="auth-hint">
          这是第一次启用鉴权。创建的账号将拥有管理员权限。
        </p>
        <t-form @submit.prevent="onSubmit"
                label-align="right"
                label-width="90px"
        >
          <t-form-item label="用户名">
            <t-input v-model="username" placeholder="用户名（3-32 位字母数字）" size="large"
                     autocomplete="username" :autofocus="true"/>
          </t-form-item>
          <t-form-item label="密码">
            <t-input v-model="password" type="password" placeholder="密码" size="large"
                     autocomplete="new-password"/>
          </t-form-item>
          <t-form-item label="确认密码">
            <t-input v-model="confirm" type="password" placeholder="确认密码" size="large"
                     autocomplete="new-password" @enter="onSubmit"/>
          </t-form-item>
          <t-form-item class="create-admin-and-login">
            <t-button theme="primary" size="large" block :loading="loading" @click="onSubmit">
              创建并登录
            </t-button>
          </t-form-item>
        </t-form>
      </template>

      <t-alert v-if="error" theme="error" :message="error" class="auth-error"/>
    </div>
  </div>
</template>

<script setup>
import {onMounted, ref} from 'vue'
import {useRouter} from 'vue-router'
import {authState, recheck} from '@/store/authStore.js'
import * as authApi from '@/apis/authApi.js'

const router = useRouter()
const username = ref('')
const password = ref('')
const confirm = ref('')
const error = ref('')
const loading = ref(false)

onMounted(async () => {
  await recheck().catch(() => {
  })
  // 已经有账号了就没必要停在这一页
  if (!authState.setupRequired) {
    router.replace(authState.authenticated ? '/' : '/login')
  }
})

async function onSubmit() {
  if (loading.value) return
  error.value = ''
  if (!username.value || !password.value) {
    error.value = '请填写用户名和密码'
    return
  }
  if (password.value !== confirm.value) {
    error.value = '两次输入的密码不一致'
    return
  }
  loading.value = true
  try {
    await authApi.setupAdmin(username.value, password.value)
    await recheck(true)
    router.replace('/')
  } catch (e) {
    error.value = e.message || '创建失败'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped lang="less">
.auth-page {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100vw;
  height: 100vh;
  padding: 24px;
  box-sizing: border-box;
  background: url("@/assets/wallhaven-dg7keo.webp") center center no-repeat;
  background-size: 100% 100%;
}

.create-admin-and-login {
  :deep(.t-form__label) {
    width: 0px !important;
  }

  :deep(.t-form__controls) {
    margin-left: 0px !important;
  }
}

.auth-card {
  width: 100%;
  max-width: 420px;
  padding: 32px;
  border-radius: 12px;
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);

  :deep(.t-form__label) {
    font-size: 16px !important;
    line-height: 40px !important;
  }
}

.auth-logo {
  text-align: center;
  margin-bottom: 24px;

  img {
    height: 106px;
  }

  h2 {
    margin: 12px 0 0;
    font-size: 18px;
    font-weight: 500;
  }
}

.auth-hint {
  margin: 0 0 16px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  text-align: center;
}

.auth-error {
  margin-top: 16px;

  code {
    display: inline-block;
    margin-top: 4px;
    padding: 2px 6px;
    border-radius: 3px;
    background: var(--td-bg-color-secondarycontainer);
    font-family: monospace;
  }
}
</style>
