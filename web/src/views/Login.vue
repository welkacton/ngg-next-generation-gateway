<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCheckbox, NInput } from 'naive-ui'
import * as api from '../api/ngg'
import type { AuthState } from '../api/ngg'

const emit = defineEmits<{ (e: 'authenticated', username: string): void }>()

const state = ref<AuthState | null>(null)
const loading = ref(false)
const booting = ref(true)
const fatal = ref('')

const username = ref('')
const password = ref('')
const confirm = ref('')
const remember = ref(true)
const errorText = ref('')
const lockLeft = ref(0)

let lockTimer: number | undefined

const setupMode = computed(() => state.value?.setup_required !== false)

const strength = computed(() => {
  const v = password.value
  if (!v) return { score: 0, label: '至少 8 位', tone: 'idle' }
  let score = 0
  if (v.length >= 8) score += 1
  if (v.length >= 12) score += 1
  if (/[a-z]/.test(v) && /[A-Z]/.test(v)) score += 1
  if (/\d/.test(v)) score += 1
  if (/[^A-Za-z0-9]/.test(v)) score += 1
  const label = score <= 2 ? '偏弱' : score <= 3 ? '一般' : score === 4 ? '较强' : '很强'
  const tone = score <= 2 ? 'weak' : score <= 3 ? 'fair' : 'good'
  return { score, label, tone }
})

const canSubmit = computed(() => {
  if (loading.value) return false
  if (!username.value.trim() || password.value.length < 8) return false
  if (setupMode.value && password.value !== confirm.value) return false
  return true
})

async function load() {
  booting.value = true
  fatal.value = ''
  try {
    state.value = await api.getAuthState()
    if (!state.value.setup_required && state.value.username) username.value = state.value.username
  } catch (e) {
    fatal.value = e instanceof Error ? e.message : String(e)
  } finally {
    booting.value = false
  }
}

function startLock(seconds: number) {
  lockLeft.value = seconds
  if (lockTimer) window.clearInterval(lockTimer)
  lockTimer = window.setInterval(() => {
    lockLeft.value -= 1
    if (lockLeft.value <= 0 && lockTimer) window.clearInterval(lockTimer)
  }, 1000)
}

function friendly(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e)
  const m = raw.match(/HTTP (\d{3})[^·]*·\s*(.*)$/)
  if (m) {
    const code = Number(m[1])
    const detail = m[2].trim()
    if (code === 429) {
      const secs = detail.match(/(\d+)\s*秒/)?.[1]
      startLock(secs ? Number(secs) : 60)
      return detail || '连续登录失败次数过多，请稍后再试'
    }
    if (code === 401) return detail || '用户名或密码不正确'
    if (code === 409) return '管理员账号已存在，请直接登录'
    return detail || raw
  }
  return raw
}

async function submit() {
  if (!canSubmit.value) return
  errorText.value = ''
  loading.value = true
  try {
    const res = setupMode.value
      ? await api.authSetup(username.value.trim(), password.value)
      : await api.authLogin(username.value.trim(), password.value, remember.value)
    password.value = ''
    confirm.value = ''
    emit('authenticated', res.username)
  } catch (e) {
    errorText.value = friendly(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="login-shell">
    <div class="login-brand">
      <div class="login-brand__glow" />
      <div class="login-brand__inner">
        <div class="login-logo">N</div>
        <h1>NGG 管理中心</h1>
        <p class="login-brand__sub">Next-Generation Gateway</p>
        <ul class="login-points">
          <li><span class="dot" />统一 TUN 出入口调度</li>
          <li><span class="dot" />多内核协议运行时</li>
          <li><span class="dot" />链路编排 · 真实流量计量</li>
        </ul>
        <p class="login-brand__note">
          控制 API 仅监听本机回环地址，登录凭据使用 Argon2id 加密存储。
        </p>
      </div>
    </div>

    <div class="login-panel">
      <div class="login-card">
        <div v-if="booting" class="login-loading">正在检查登录状态…</div>

        <div v-else-if="fatal" class="login-error-state">
          <h2>无法连接后端</h2>
          <p class="hint">{{ fatal }}</p>
          <NButton size="small" @click="load">重试</NButton>
        </div>

        <template v-else>
          <header class="login-head">
            <div class="login-eyebrow">
              {{ setupMode ? '首次启动 · 仅此一次' : '安全登录' }}
            </div>
            <h2>{{ setupMode ? '创建管理员账号' : '登录管理中心' }}</h2>
            <p class="login-desc">
              {{
                setupMode
                  ? '这台设备还没有管理员。请手动创建一个账号，之后打开网页只会看到登录窗口。'
                  : '请输入管理员账号与密码。会话默认保留 7 天，勾选「记住我」可延长到 30 天。'
              }}
            </p>
          </header>

          <div class="login-form">
            <label class="field">
              <span>用户名</span>
              <NInput
                v-model:value="username"
                placeholder="例如 admin"
                :maxlength="32"
                :disabled="!setupMode && !!state?.username"
                @keyup.enter="submit"
              />
            </label>

            <label class="field">
              <span>密码</span>
              <NInput
                v-model:value="password"
                type="password"
                show-password-on="click"
                :placeholder="setupMode ? '至少 8 位，建议包含大小写与符号' : '请输入密码'"
                @keyup.enter="submit"
              />
            </label>

            <div v-if="setupMode" class="strength">
              <div class="strength-bar">
                <i
                  v-for="n in 5"
                  :key="n"
                  :class="[
                    'strength-seg',
                    n <= strength.score ? `on ${strength.tone}` : '',
                  ]"
                />
              </div>
              <span :class="['strength-label', strength.tone]">{{ strength.label }}</span>
            </div>

            <label v-if="setupMode" class="field">
              <span>确认密码</span>
              <NInput
                v-model:value="confirm"
                type="password"
                show-password-on="click"
                placeholder="再次输入密码"
                @keyup.enter="submit"
              />
            </label>
            <p v-if="setupMode && confirm && confirm !== password" class="field-error">
              两次输入的密码不一致
            </p>

            <NCheckbox v-if="!setupMode" v-model:checked="remember">记住我（30 天）</NCheckbox>

            <NAlert v-if="errorText" type="error" :bordered="false" class="login-alert">
              {{ errorText }}
            </NAlert>
            <p v-if="lockLeft > 0" class="hint">
              已锁定，{{ lockLeft }} 秒后可再次尝试。
            </p>

            <NButton
              type="primary"
              size="large"
              block
              :loading="loading"
              :disabled="!canSubmit || lockLeft > 0"
              @click="submit"
            >
              {{ setupMode ? '创建账号并进入' : '登录' }}
            </NButton>

            <p class="login-foot">
              <template v-if="setupMode">
                创建后本机浏览器会保持登录；忘记密码可在数据目录删除
                <span class="mono">auth.json</span> 后重新初始化。
              </template>
              <template v-else>
                连续 5 次输入错误会锁定 1 分钟。
              </template>
            </p>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-shell {
  display: grid;
  grid-template-columns: minmax(360px, 0.9fr) minmax(420px, 1.1fr);
  min-height: 100vh;
  background: #f5f5f7;
}

.login-brand {
  position: relative;
  overflow: hidden;
  background: linear-gradient(160deg, #0b1220 0%, #12233c 46%, #1d3f6b 100%);
  color: #fff;
  display: flex;
  align-items: center;
}
.login-brand__glow {
  position: absolute;
  inset: -30% -20% auto -30%;
  height: 620px;
  background: radial-gradient(circle at 30% 30%, rgba(0, 113, 227, 0.55), transparent 62%);
  filter: blur(10px);
}
.login-brand__inner {
  position: relative;
  padding: 0 clamp(28px, 5vw, 64px);
  max-width: 520px;
}
.login-logo {
  width: 46px;
  height: 46px;
  border-radius: 13px;
  background: linear-gradient(140deg, #fff, #d7e6ff);
  color: #0b1220;
  font-weight: 700;
  font-size: 22px;
  display: grid;
  place-items: center;
  margin-bottom: 26px;
}
.login-brand h1 {
  margin: 0 0 6px;
  font-size: clamp(24px, 2.4vw, 32px);
  letter-spacing: -0.02em;
}
.login-brand__sub {
  margin: 0 0 30px;
  color: rgba(255, 255, 255, 0.62);
  font-size: 13px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
.login-points {
  list-style: none;
  padding: 0;
  margin: 0 0 34px;
  display: grid;
  gap: 14px;
}
.login-points li {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
  color: rgba(255, 255, 255, 0.86);
}
.login-points .dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #64a8ff;
  box-shadow: 0 0 0 4px rgba(100, 168, 255, 0.16);
}
.login-brand__note {
  margin: 0;
  font-size: 12px;
  line-height: 1.7;
  color: rgba(255, 255, 255, 0.5);
  border-top: 1px solid rgba(255, 255, 255, 0.12);
  padding-top: 18px;
}

.login-panel {
  display: grid;
  place-items: center;
  padding: 40px 24px;
}
.login-card {
  width: 100%;
  max-width: 400px;
}
.login-head h2 {
  margin: 8px 0 10px;
  font-size: 26px;
  letter-spacing: -0.02em;
  color: #1d1d1f;
}
.login-eyebrow {
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.08em;
  color: #0071e3;
  text-transform: uppercase;
}
.login-desc {
  margin: 0 0 26px;
  font-size: 13px;
  line-height: 1.7;
  color: #6e6e73;
}
.login-form {
  display: grid;
  gap: 16px;
}
.field {
  display: grid;
  gap: 7px;
  font-size: 12.5px;
  color: #6e6e73;
}
.field :deep(.n-input) {
  --n-height: 42px !important;
  border-radius: 10px;
}
.strength {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: -6px;
}
.strength-bar {
  display: flex;
  gap: 4px;
  flex: 1;
}
.strength-seg {
  height: 4px;
  flex: 1;
  border-radius: 2px;
  background: #e5e5ea;
}
.strength-seg.on.weak {
  background: #ff453a;
}
.strength-seg.on.fair {
  background: #ff9f0a;
}
.strength-seg.on.good {
  background: #30d158;
}
.strength-label {
  font-size: 11.5px;
  color: #8e8e93;
  min-width: 32px;
}
.strength-label.weak {
  color: #ff453a;
}
.strength-label.fair {
  color: #ff9f0a;
}
.strength-label.good {
  color: #248a3d;
}
.field-error {
  margin: -8px 0 0;
  font-size: 12px;
  color: #ff453a;
}
.login-alert {
  border-radius: 10px;
}
.login-foot {
  margin: 4px 0 0;
  font-size: 11.5px;
  line-height: 1.7;
  color: #8e8e93;
}
.login-loading,
.login-error-state {
  text-align: center;
  color: #6e6e73;
}
.login-error-state h2 {
  font-size: 18px;
  color: #1d1d1f;
}
.hint {
  font-size: 12px;
  color: #8e8e93;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
@media (max-width: 900px) {
  .login-shell {
    grid-template-columns: 1fr;
  }
  .login-brand {
    min-height: 240px;
  }
  .login-brand__inner {
    padding: 32px 24px;
  }
  .login-points {
    display: none;
  }
}
</style>
