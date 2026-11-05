import { describe, expect, it } from 'vitest'
import { parseRateSetValue } from '../rateMultiplierSet'

describe('parseRateSetValue', () => {
  it.each([null, undefined, '', '   '])('rejects empty input %j', (raw) => {
    expect(parseRateSetValue(raw)).toEqual({ ok: false, reason: 'empty' })
  })

  it.each([Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY, 'abc'])(
    'rejects non-finite input %j',
    (raw) => {
      expect(parseRateSetValue(raw)).toEqual({ ok: false, reason: 'not-finite' })
    }
  )

  it.each([0, -0.5, -1, '0', '-1'])('rejects non-positive input %j', (raw) => {
    expect(parseRateSetValue(raw)).toEqual({ ok: false, reason: 'not-positive' })
  })

  it.each([1e-7, 0.0000004, '0.0000004'])('rejects values that round to zero %j', (raw) => {
    expect(parseRateSetValue(raw)).toEqual({ ok: false, reason: 'rounds-to-zero' })
  })

  it('keeps six-decimal precision used by the local draft', () => {
    expect(parseRateSetValue(1.23456789)).toEqual({ ok: true, value: 1.234568 })
    expect(parseRateSetValue('0.000001')).toEqual({ ok: true, value: 0.000001 })
  })

  it.each([
    [2, 2],
    ['0.5', 0.5],
    [0.001, 0.001]
  ])('accepts positive input %j', (raw, expected) => {
    expect(parseRateSetValue(raw)).toEqual({ ok: true, value: expected })
  })
})
