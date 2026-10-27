import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'node:path'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    proxy: { '/v1': 'http://127.0.0.1:18610', '/admin': 'http://127.0.0.1:18610', '/healthz': 'http://127.0.0.1:18610' },
  },
  build: {
    outDir: path.resolve(__dirname, '../internal/server/static'),
    emptyOutDir: true,
    rollupOptions: { output: { manualChunks: {
      react: ['react', 'react-dom', 'react-router-dom'],
      data: ['@tanstack/react-query'],
      forms: ['react-hook-form', 'zod', '@hookform/resolvers'],
      radix: ['@radix-ui/react-dialog', '@radix-ui/react-dropdown-menu'],
    } } },
  },
  test: { environment: 'jsdom', setupFiles: './src/test/setup.ts', css: true },
})
