<template>
  <div class="auth-page">
    <div class="auth-card">
      <div class="auth-logo">
        <img src="/ASA_Logo_transparent.webp" alt="ASA">
        <h2>ASA 服务器管理</h2>
      </div>

      <!-- 第一步：密码。始终是主路径，永远存在。 -->
      <template v-if="stage === 'password'">
        <t-form label-align="right"
                label-width="70px"
        >
          <t-form-item label="用户名">
            <t-input
                v-model="username"
                placeholder="用户名"
                size="large"
                autocomplete="username"
                :autofocus="true"
                @enter="onSubmitPassword"
            />
          </t-form-item>
          <t-form-item label="密码">
            <t-input
                v-model="password"
                type="password"
                placeholder="密码"
                size="large"
                autocomplete="current-password"
                ref="passwordRef"
                @enter="onSubmitPassword"
            />
          </t-form-item>
          <t-form-item class="login-btn">
            <t-button
                theme="primary"
                size="large"
                block
                :loading="loading"
                @click="onSubmitPassword"
            >
              登录
            </t-button>
          </t-form-item>
        </t-form>

      </template>

      <!-- 第二步：两步验证 -->
      <template v-else>
        <p class="auth-hint">
          请输入验证器 App 中的 6 位验证码，或一个恢复码。
        </p>
        <t-form>
          <t-form-item>
            <t-input
                v-model="code"
                placeholder="6 位验证码 或 XXXX-XXXX-XXXX"
                size="large"
                autocomplete="one-time-code"
                :autofocus="true"
                @enter="onSubmitTOTP"
            />
          </t-form-item>
          <t-button theme="primary" size="large" block :loading="loading" @click="onSubmitTOTP">
            验证
          </t-button>
        </t-form>
        <t-button variant="text" size="small" block @click="backToPassword">
          返回
        </t-button>
      </template>

      <t-alert v-if="error" theme="error" :message="error" class="auth-error"/>
    </div>
  </div>
</template>

<script setup>
import {computed, nextTick, onMounted, ref} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {authState, doLogin, doLoginTOTP, recheck} from '@/store/authStore.js'

const router = useRouter()
const route = useRoute()

const stage = ref('password')
const username = ref('')
const password = ref('')
const code = ref('')
const error = ref('')
const loading = ref(false)
const passwordRef = ref(null)

onMounted(async () => {
  if (!authState.ready) {
    await recheck().catch(() => {
    })
  }
})

function goNext() {
  const redirect = route.query.redirect
  router.replace(typeof redirect === 'string' && redirect !== '/login' ? redirect : '/')
}

async function onSubmitPassword() {
  if (loading.value) return
  error.value = ''
  if (!username.value || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  loading.value = true
  try {
    const res = await doLogin(username.value, password.value)
    if (res.totpRequired) {
      stage.value = 'totp'
      code.value = ''
    } else {
      goNext()
    }
  } catch (e) {
    error.value = e.message || '登录失败'
  } finally {
    loading.value = false
  }
}

async function onSubmitTOTP() {
  if (loading.value) return
  error.value = ''
  if (!code.value) {
    error.value = '请输入验证码'
    return
  }
  loading.value = true
  try {
    await doLoginTOTP(code.value)
    goNext()
  } catch (e) {
    error.value = e.message || '验证失败'
  } finally {
    loading.value = false
  }
}

function backToPassword() {
  stage.value = 'password'
  error.value = ''
  password.value = ''
}
</script>

<style scoped lang="less">
.auth-page {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  width: 100vw;
  height: 100vh;
  box-sizing: border-box;
  background: url("@/assets/wallhaven-dg7keo.webp") center center no-repeat;
  background-size: 100% 100%;
}

.login-btn {
  :deep(.t-form__label) {
    width: 0px !important;
  }

  :deep(.t-form__controls) {
    margin-left: 0px !important;
  }
}

.auth-card {
  width: 100%;
  max-width: 380px;
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
}

.auth-error {
  margin-top: 16px;
}
</style>
