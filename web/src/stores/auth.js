import { defineStore } from 'pinia'
import { api } from '../net/http.js'
import { getToken, setToken, clearToken } from '../net/token.js'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: getToken(),
    user: null, // { id, username }
  }),
  actions: {
    async login(username, password) {
      const r = await api('/api/login', { method: 'POST', body: { username, password } })
      setToken(r.token)
      this.token = r.token
      this.user = r.user
    },
    async register(username, password) {
      await api('/api/register', { method: 'POST', body: { username, password } })
      await this.login(username, password)
    },
    async fetchMe() {
      this.user = await api('/api/me')
      return this.user
    },
    logout() {
      clearToken()
      this.token = ''
      this.user = null
    },
  },
})
