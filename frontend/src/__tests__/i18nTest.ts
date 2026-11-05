import en from '@/i18n/locales/en/tickets'

const flat = new Map<string, string>()

function walk(prefix: string, value: unknown) {
  if (typeof value === 'string') {
    flat.set(prefix, value)
    return
  }
  if (value && typeof value === 'object') {
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      walk(prefix ? `${prefix}.${key}` : key, child)
    }
  }
}

walk('', en)

/**
 * Minimal vue-i18n replacement for component tests.
 *
 * The Vitest alias resolves vue-i18n to its runtime-only build, which cannot
 * compile the string messages used by the application locales. This helper keeps
 * assertions on the real English strings without shipping a compiler to tests.
 */
export const testI18n = {
  locale: { value: 'en' },
  t(key: string, params?: Record<string, unknown>): string {
    let value = flat.get(key) ?? key
    if (params) {
      for (const [name, replacement] of Object.entries(params)) {
        value = value.replaceAll(`{${name}}`, String(replacement))
      }
    }
    return value
  },
  te(key: string): boolean {
    return flat.has(key)
  }
}
