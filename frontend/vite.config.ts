import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Go отдаёт HTML-оболочку сам (internal/platform/view), поэтому index.html не нужен:
// точка входа задаётся через rollupOptions.input и должна совпадать с entryPoint в view.go.
export default defineConfig({
  plugins: [vue()],
  base: '/',
  build: {
    manifest: true, // dist/.vite/manifest.json: из него Go берёт имена файлов с хэшами
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: { input: 'src/main.ts' },
  },
  server: {
    host: true,
    port: 5173,
    strictPort: true,
    // Страница открывается с Go-сервера, а скрипты грузятся с :5173 — нужен CORS.
    cors: true,
  },
})
