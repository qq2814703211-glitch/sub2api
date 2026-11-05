import { beforeEach, describe, expect, it, vi } from 'vitest'
import { create, update } from '../keys'
import { updateApiKeyRoutes } from '../admin/apiKeys'
const { post, put } = vi.hoisted(() => ({ post: vi.fn(), put: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { post, put } }))
beforeEach(() => { post.mockResolvedValue({ data: {} }); put.mockResolvedValue({ data: {} }) })
describe('ordered API key route payloads', () => {
  it('creates ordered candidates and preserves group_id primary compatibility', async () => {
    await create('routed', 3, undefined, undefined, undefined, undefined, undefined, undefined, [3, 1, 20])
    expect(post).toHaveBeenCalledWith('/keys', { name: 'routed', group_id: 3, group_ids: [3, 1, 20] })
  })
  it('does not rewrite legacy create payloads', async () => {
    await create('legacy', 1)
    expect(post).toHaveBeenCalledWith('/keys', { name: 'legacy', group_id: 1 })
  })
  it('keeps order for user and admin updates including empty unbind', async () => {
    await update(7, { group_id: 20, group_ids: [20, 3] })
    expect(put).toHaveBeenCalledWith('/keys/7', { group_id: 20, group_ids: [20, 3] })
    await updateApiKeyRoutes(7, [])
    expect(put).toHaveBeenCalledWith('/admin/api-keys/7', { group_ids: [] })
  })
})
