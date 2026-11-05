import { describe, expect, it } from 'vitest'
import { isClientID, newClientID } from '../tickets'

describe('ticket client ids', () => {
  it('generates nonzero RFC4122 v4 ids accepted by the backend contract', () => {
    const ids = new Set<string>()
    for (let i = 0; i < 64; i += 1) {
      const id = newClientID()
      expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
      expect(isClientID(id)).toBe(true)
      ids.add(id)
    }
    expect(ids.size).toBe(64)
  })

  it('rejects compact, nil and malformed ids', () => {
    expect(isClientID('00000000-0000-0000-0000-000000000000')).toBe(false)
    expect(isClientID('6b7a9b041952491cae6da2da990d8a8b')).toBe(false)
    expect(isClientID('not-a-uuid')).toBe(false)
  })
})
