<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth.js'

const auth = useAuthStore()
const router = useRouter()
const mode = ref('login')
const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

async function submit() {
  error.value = ''
  busy.value = true
  try {
    if (mode.value === 'login') await auth.login(username.value.trim(), password.value)
    else await auth.register(username.value.trim(), password.value)
    router.push('/')
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login">
    <form class="card" @submit.prevent="submit">
      <h2>IM</h2>
      <div class="tabs">
        <button type="button" :class="{ on: mode === 'login' }" @click="mode = 'login'">登录</button>
        <button type="button" :class="{ on: mode === 'register' }" @click="mode = 'register'">注册</button>
      </div>
      <input v-model="username" placeholder="用户名（3-32 位字母数字下划线）" autocomplete="username" required />
      <input
        v-model="password"
        type="password"
        placeholder="密码（至少 6 位）"
        :autocomplete="mode === 'login' ? 'current-password' : 'new-password'"
        required
      />
      <p v-if="error" class="error">{{ error }}</p>
      <button class="primary" :disabled="busy">{{ mode === 'login' ? '登录' : '注册并登录' }}</button>
    </form>
  </div>
</template>

<style scoped>
.login {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
.card {
  width: 320px;
  background: var(--panel);
  padding: 28px;
  border-radius: 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.08);
}
h2 {
  margin: 0;
  text-align: center;
}
.tabs {
  display: flex;
  gap: 8px;
}
.tabs button {
  flex: 1;
}
.tabs .on {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
}
.error {
  color: var(--danger);
  margin: 0;
  font-size: 13px;
}
</style>
