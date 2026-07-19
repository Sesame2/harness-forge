import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://control-plane:8080',
      '/health': 'http://control-plane:8080',
    },
  },
  test: {
    environment: 'jsdom',
  },
})
