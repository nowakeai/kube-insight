import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig, loadEnv } from 'vite'

const repoRoot = fileURLToPath(new URL('..', import.meta.url))

export default defineConfig(({ mode }) => {
  const rootEnv = loadEnv(mode, repoRoot, '')
  const allowedHosts = parseAllowedHosts(
    process.env.KUBE_INSIGHT_WEB_ALLOWED_HOSTS
      ?? rootEnv.KUBE_INSIGHT_WEB_ALLOWED_HOSTS
      ?? process.env.VITE_ALLOWED_HOSTS
      ?? rootEnv.VITE_ALLOWED_HOSTS
      ?? '',
  )

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    server: allowedHosts.length > 0 ? { allowedHosts } : undefined,
  }
})

function parseAllowedHosts(value: string) {
  return Array.from(
    new Set(
      value
        .split(/[\s,]+/)
        .map((host) => normalizeAllowedHost(host))
        .filter((host): host is string => Boolean(host)),
    ),
  )
}

function normalizeAllowedHost(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  if (trimmed.startsWith('.')) return trimmed
  try {
    return new URL(trimmed.includes('://') ? trimmed : `http://${trimmed}`).hostname
  } catch {
    return trimmed.split('/')[0].split(':')[0] || undefined
  }
}
