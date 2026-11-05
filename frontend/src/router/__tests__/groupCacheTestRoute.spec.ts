// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import { describe, expect, it } from 'vitest'
import router from '@/router'

describe('group cache test route', () => {
  it('registers the panel as an admin route with a localized title', () => {
    const resolved = router.resolve('/admin/group-cache-tests')
    expect(resolved.name).toBe('AdminGroupCacheTests')
    expect(resolved.meta.requiresAuth).toBe(true)
    expect(resolved.meta.requiresAdmin).toBe(true)
    expect(resolved.meta.titleKey).toBe('admin.groupCacheTest.title')
  })
})
