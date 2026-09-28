import { defineStore } from 'pinia'
import { api } from '../net/http.js'

const INTERVAL = 30000
let timer = null

export const usePresenceStore = defineStore('presence', {
  state: () => ({
    online: {}, // uid -> bool
  }),
  actions: {
    async refresh(uids) {
      const ids = [...new Set(uids)].filter(Boolean)
      if (!ids.length) return
      try {
        const m = await api(`/api/presence?uids=${ids.join(',')}`)
        for (const [k, v] of Object.entries(m)) this.online[k] = v
      } catch {
        // 在线状态只是提示，查询失败时保留上一次的结果
      }
    },
    // getUids 每次现取，这样会话列表变化后自动覆盖新用户
    start(getUids) {
      this.stop()
      this.refresh(getUids())
      timer = setInterval(() => this.refresh(getUids()), INTERVAL)
    },
    stop() {
      clearInterval(timer)
      timer = null
    },
  },
})
