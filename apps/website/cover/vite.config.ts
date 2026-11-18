import { defineConfig } from 'vite'

// Serves the README cover's capture page (docs/development.md, README cover); never built.
export default defineConfig({
  root: import.meta.dirname,
  server: { host: '127.0.0.1', strictPort: false },
})
