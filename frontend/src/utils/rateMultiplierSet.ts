// 分组专属倍率“统一设值”的输入归一化。
// 语义参考 kiss-kedaya GroupRateMultipliersModal.vue:459 applyBatchSet（f01772586）：
// 只接受有限正数，并按界面真实精度（6 位小数）取整；舍入后 <= 0 的值不能保存。
export const RATE_SET_DECIMALS = 6

export type RateSetParseResult =
  | { ok: true; value: number }
  | { ok: false; reason: 'empty' | 'not-finite' | 'not-positive' | 'rounds-to-zero' }

export function parseRateSetValue(raw: unknown): RateSetParseResult {
  if (raw === null || raw === undefined) return { ok: false, reason: 'empty' }
  if (typeof raw === 'string' && raw.trim() === '') return { ok: false, reason: 'empty' }

  const numeric = typeof raw === 'number' ? raw : Number(String(raw).trim())
  if (!Number.isFinite(numeric)) return { ok: false, reason: 'not-finite' }
  if (numeric <= 0) return { ok: false, reason: 'not-positive' }

  const rounded = Number(numeric.toFixed(RATE_SET_DECIMALS))
  if (rounded <= 0) return { ok: false, reason: 'rounds-to-zero' }

  return { ok: true, value: rounded }
}
