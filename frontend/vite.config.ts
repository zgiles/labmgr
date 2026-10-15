import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// LABMGR_API lets `npm run dev` point at a backend other than localhost:8098
const api = process.env.LABMGR_API || 'http://localhost:8098'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: 'dist',
  },
  server: {
    host: '0.0.0.0',
    port: Number(process.env.PORT) || 8099,
    proxy: {
      '/api': { target: api, ws: true },
    },
  },
})
