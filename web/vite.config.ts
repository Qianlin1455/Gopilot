import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const proxy = {
  '/demo': {
    target: 'http://localhost:8081',
    changeOrigin: true,
    rewrite: (path: string) => path.replace(/^\/demo/, ''),
  },
  '/gopilot': {
    target: 'http://localhost:8080',
    changeOrigin: true,
    rewrite: (path: string) => path.replace(/^\/gopilot/, ''),
  },
}

export default defineConfig({
  plugins: [react()],
  server: { proxy },
  preview: { proxy },
})
