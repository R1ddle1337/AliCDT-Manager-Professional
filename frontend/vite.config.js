import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  test: {
    exclude: ['**/node_modules/**', '**/dist/**', '**/e2e/**']
  },
  server: {
    proxy: {
      '/api/v2': 'http://localhost:18010',
      '/api': 'http://localhost:18010'
    }
  }
})
