import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: { port: 5173 },
  build: {
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('@tanstack/react-query')) return 'query'
          if (id.includes('lucide-react')) return 'icons'
          if (id.includes('react')) return 'react'
          return 'vendor'
        },
      },
    },
  },
})
