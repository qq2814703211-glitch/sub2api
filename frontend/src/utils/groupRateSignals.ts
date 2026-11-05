import type { Group } from '@/types'

/**
 * GET /groups/available 附加在分组上的展示提示。它们不参与计价；
 * 字段缺失只表示「没有可展示的内容」。
 */
export type GroupSignalSource = Pick<
  Group,
  'recent_rate_change' | 'is_new' | 'launched_at' | 'launch_rate'
>

/** 去掉多余的尾零，但至少保留两位小数：0.5 -> "0.50"，0.0125 -> "0.0125"。 */
export function formatRateValue(value: number): string {
  if (!Number.isFinite(value)) return '-'
  const fixed = value.toFixed(4)
  const trimmed = fixed.replace(/0+$/, '')
  const [integer, fraction = ''] = trimmed.split('.')
  return `${integer}.${fraction.padEnd(2, '0')}`
}

/** 调价或上线时刻的本地短日期时间；无法解析时返回 "-"。 */
export function formatSignalTime(value: string | null | undefined, locale?: string): string {
  if (!value) return '-'
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) return '-'
  return new Intl.DateTimeFormat(locale || undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(parsed)
}
