import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// VITE_BACKEND 让同一份前端连不同的后端实例（A: 8080，B: 8081），用来测跨实例聊天
const backend = process.env.VITE_BACKEND || 'localhost:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': `http://${backend}`,
      '/files': `http://${backend}`,
      '/ws': { target: `ws://${backend}`, ws: true },
    },
  },
})
