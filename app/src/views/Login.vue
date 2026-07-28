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

        <!-- Passkey 只是补充入口，仅在服务端判定可用时才渲染。
             不可用（IP 访问、域名不在白名单、非 HTTPS）时这里什么都不显示，
             用户继续用上面的密码表单，不会看到任何报错。 -->
        <template v-if="passkeyUsable">
          <div class="auth-divider"><span>或</span></div>
          <t-button
              variant="outline"
              size="large"
              block
              :loading="passkeyLoading"
              @click="onPasskeyLogin"
          >
            🔑 {{ platformAuthenticator ? '使用本机生物识别登录' : '使用 Passkey 登录' }}
          </t-button>
        </template>
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
import {isWebAuthnSupported, isPlatformAuthenticatorAvailable, passkeyLogin} from '@/utils/webauthn.js'

const router = useRouter()
const route = useRoute()

const stage = ref('password')
const username = ref('')
const password = ref('')
const code = ref('')
const error = ref('')
const loading = ref(false)
const passkeyLoading = ref(false)
const passwordRef = ref(null)
const platformAuthenticator = ref(false)

// 可用性判定由后端下发（它知道域名白名单、是否安全上下文）；
// 浏览器支不支持只有前端知道，所以两边都要看。
const passkeyUsable = computed(() => authState.webauthnAvailable && isWebAuthnSupported())

onMounted(async () => {
  if (!authState.ready) {
    await recheck().catch(() => {
    })
  }
  if (passkeyUsable.value) {
    platformAuthenticator.value = await isPlatformAuthenticatorAvailable()
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

async function onPasskeyLogin() {
  if (passkeyLoading.value) return
  error.value = ''
  passkeyLoading.value = true
  try {
    await passkeyLogin()
    await recheck(true)
    goNext()
  } catch (e) {
    // 用户取消系统弹窗不是错误，静默回到密码表单即可。
    // 这一条也确保它不会被计入登录失败限流（后端同样不计）。
    if (e.name === 'NotAllowedError' || e.name === 'AbortError') {
      return
    }
    error.value = 'Passkey 验证失败，请使用密码登录'
    // 把焦点交回密码框，别把用户卡在一个只能重试 Passkey 的界面上
    await nextTick()
    passwordRef.value?.focus?.()
  } finally {
    passkeyLoading.value = false
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

.auth-divider {
  display: flex;
  align-items: center;
  margin: 20px 0 16px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;

  &::before, &::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--td-component-stroke);
  }

  span {
    padding: 0 12px;
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
