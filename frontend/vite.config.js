import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api/v2': 'http://localhost:18010',
      '/api': 'http://localhost:8000'
    }
  }
})
