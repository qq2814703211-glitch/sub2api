import type { Ref } from 'vue'
import type { ApiKey, Group } from '@/types'
import type { BatchApiKeyUsageStats } from '@/api/usage'
import type { BulkDeleteKeysResult } from '@/api/keys'

export interface KeyPaginationState {
  page: number
  page_size: number
  total: number
  pages: number
}

export interface RemovedKeySnapshot {
  index: number
  key: ApiKey
  usage?: BatchApiKeyUsageStats
}

export interface KeyRowMutationResult {
  succeeded: number[]
  failed: number[]
  error?: unknown
}

export interface UseKeyRowActionsOptions {
  apiKeys: Ref<ApiKey[]>
  usageStats: Ref<Record<string, BatchApiKeyUsageStats>>
  selectedIds: Ref<number[]>
  selectedKey: Ref<ApiKey | null>
  pagination: Ref<KeyPaginationState>
  groups: Ref<Group[]>
  matchesFilters: (key: ApiKey) => boolean
  compareRows: (left: ApiKey, right: ApiKey) => number
  loadPage: () => Promise<void>
  /** Bump the list generation and abort any list request started before the mutation. */
  invalidateListLoad: () => void
  deleteKey: (id: number) => Promise<unknown>
  deleteKeys: (ids: number[]) => Promise<BulkDeleteKeysResult>
}

const normalizeIds = (ids: number[]): number[] =>
  [...new Set(ids.map(Number).filter((id) => Number.isFinite(id)))]

/**
 * Local row mutations for the user Keys table. Keeps list/usage/pagination in
 * sync from server responses so actions do not refetch a page unless a page
 * actually needs backfilling, and rolls optimistic deletes back on failure.
 */
export function useKeyRowActions(options: UseKeyRowActionsOptions) {
  const {
    apiKeys,
    usageStats,
    selectedIds,
    selectedKey,
    pagination,
    groups,
    matchesFilters,
    compareRows,
    loadPage,
    invalidateListLoad,
    deleteKey,
    deleteKeys
  } = options

  /** Adjust the server-side total locally instead of refetching the page. */
  const applyTotalDelta = (delta: number) => {
    if (delta === 0) return
    const total = Math.max(0, pagination.value.total + delta)
    pagination.value.total = total
    pagination.value.pages =
      pagination.value.page_size > 0 ? Math.ceil(total / pagination.value.page_size) : 0
    if (total === 0) pagination.value.page = 1
  }

  const removeKeysLocally = (ids: number[]): RemovedKeySnapshot[] => {
    const idSet = new Set(normalizeIds(ids))
    if (idSet.size === 0) return []
    const removed: RemovedKeySnapshot[] = []
    apiKeys.value.forEach((key, index) => {
      if (idSet.has(key.id)) {
        removed.push({ index, key, usage: usageStats.value[key.id] })
      }
    })
    if (removed.length === 0) return removed
    invalidateListLoad()
    apiKeys.value = apiKeys.value.filter((key) => !idSet.has(key.id))
    removed.forEach((snapshot) => {
      delete usageStats.value[snapshot.key.id]
    })
    selectedIds.value = selectedIds.value.filter((id) => !idSet.has(Number(id)))
    if (selectedKey.value && idSet.has(selectedKey.value.id)) selectedKey.value = null
    return removed
  }

  const restoreRemovedKeys = (
    removed: RemovedKeySnapshot[],
    removedContext: RemovedKeySnapshot[] = removed
  ) => {
    if (removed.length === 0) return
    const next = [...apiKeys.value]
    const present = new Set(next.map((key) => key.id))
    const restoring = new Set(removed.map((snapshot) => snapshot.key.id))
    // Rows that were originally before this one but stay deleted must not
    // shift it to the right of rows that were originally after it.
    const skippedBefore = (index: number) =>
      removedContext.filter((snapshot) => snapshot.index < index && !restoring.has(snapshot.key.id)).length
    for (const snapshot of [...removed].sort((a, b) => a.index - b.index)) {
      if (present.has(snapshot.key.id)) continue
      const target = Math.max(0, snapshot.index - skippedBefore(snapshot.index))
      next.splice(Math.min(target, next.length), 0, snapshot.key)
      present.add(snapshot.key.id)
      if (snapshot.usage) usageStats.value[snapshot.key.id] = snapshot.usage
    }
    apiKeys.value = next
  }

  /** Put failed rows back into the selection so retrying only needs one click. */
  const restoreSelection = (ids: number[]) => {
    const visible = new Set(apiKeys.value.map((key) => key.id))
    const merged = new Set([...selectedIds.value, ...normalizeIds(ids)])
    selectedIds.value = [...merged].filter((id) => visible.has(id))
  }

  /**
   * Rows were already removed locally. Reload exactly one page only when the
   * current page no longer exists or has emptied out while the filtered total
   * still has keys.
   */
  const backfillPageIfEmpty = async () => {
    if (pagination.value.total <= 0) {
      if (pagination.value.page !== 1) pagination.value.page = 1
      return
    }
    if (pagination.value.pages > 0 && pagination.value.page > pagination.value.pages) {
      pagination.value.page = pagination.value.pages
      await loadPage()
      return
    }
    if (apiKeys.value.length > 0) return
    if (pagination.value.page > 1) pagination.value.page -= 1
    await loadPage()
  }

  /** Patch one row in place from a server response; never reloads the list. */
  const mergeKeyLocally = (updated: ApiKey | null | undefined): ApiKey | null => {
    if (!updated || typeof updated !== 'object' || typeof updated.id !== 'number') return null
    const index = apiKeys.value.findIndex((key) => key.id === updated.id)
    if (index === -1) return null
    // A list request started before this response could still hold the old row.
    invalidateListLoad()
    const merged: ApiKey = { ...apiKeys.value[index] }
    const patch = updated as unknown as Record<string, unknown>
    const target = merged as unknown as Record<string, unknown>
    for (const [field, value] of Object.entries(patch)) {
      if (value === undefined) continue
      // `group`/`user` can come back empty on update responses; keep the badge we already have.
      if (value === null && (field === 'group' || field === 'user')) continue
      target[field] = value
    }
    if (merged.group_id == null) {
      merged.group = undefined
    } else {
      const localGroup = groups.value.find((group) => group.id === merged.group_id)
      if (localGroup) merged.group = localGroup
    }
    apiKeys.value[index] = merged
    if (selectedKey.value?.id === merged.id) selectedKey.value = merged
    return merged
  }

  /** Insert a freshly created key without refetching the page. */
  const insertKeyLocally = (created: ApiKey | null | undefined): boolean => {
    if (!created || typeof created !== 'object' || typeof created.id !== 'number') return false
    invalidateListLoad()
    const resolvedGroup =
      created.group ?? (created.group_id == null
        ? undefined
        : groups.value.find((group) => group.id === created.group_id))
    const createdRow: ApiKey = { ...created, group: resolvedGroup }
    applyTotalDelta(1)
    if (pagination.value.page !== 1 || !matchesFilters(createdRow)) return false
    const found = apiKeys.value.findIndex((key) => compareRows(key, createdRow) > 0)
    const insertAt = found === -1 ? apiKeys.value.length : found
    const next = [...apiKeys.value]
    next.splice(insertAt, 0, createdRow)
    if (pagination.value.page_size > 0 && next.length > pagination.value.page_size) {
      // The server would push the overflow to page 2. If the new row itself
      // sorts onto the next page, leave the current page untouched.
      if (insertAt >= pagination.value.page_size) return false
      next.splice(pagination.value.page_size)
    }
    apiKeys.value = next
    return true
  }

  const deleteKeyLocally = async (
    key: ApiKey
  ): Promise<{ ok: boolean; error?: unknown }> => {
    const wasSelected = selectedIds.value.includes(key.id)
    const removed = removeKeysLocally([key.id])
    applyTotalDelta(-removed.length)
    try {
      await deleteKey(key.id)
      // Drop list responses that started before the delete completed.
      invalidateListLoad()
      await backfillPageIfEmpty()
      return { ok: true }
    } catch (error) {
      restoreRemovedKeys(removed)
      applyTotalDelta(removed.length)
      if (wasSelected) restoreSelection([key.id])
      return { ok: false, error }
    }
  }

  const deleteKeysLocally = async (ids: number[]): Promise<KeyRowMutationResult> => {
    const unique = normalizeIds(ids)
    if (unique.length === 0) return { succeeded: [], failed: [] }
    const removed = removeKeysLocally(unique)
    applyTotalDelta(-removed.length)
    try {
      const result = await deleteKeys(unique)
      // Drop list responses that started before the deletes completed.
      invalidateListLoad()
      const succeededSet = new Set(
        result.succeeded.map(Number).filter((id) => unique.includes(id))
      )
      // Anything not explicitly confirmed successful goes back on the table.
      const failedIds = unique.filter((id) => !succeededSet.has(id))
      if (failedIds.length > 0) {
        const failedSet = new Set(failedIds)
        const failedRows = removed.filter((snapshot) => failedSet.has(snapshot.key.id))
        restoreRemovedKeys(failedRows, removed)
        applyTotalDelta(failedRows.length)
        restoreSelection(failedIds)
      }
      await backfillPageIfEmpty()
      return { succeeded: [...succeededSet], failed: failedIds }
    } catch (error) {
      restoreRemovedKeys(removed)
      applyTotalDelta(removed.length)
      restoreSelection(unique)
      return { succeeded: [], failed: unique, error }
    }
  }

  return {
    applyTotalDelta,
    removeKeysLocally,
    restoreRemovedKeys,
    restoreSelection,
    backfillPageIfEmpty,
    mergeKeyLocally,
    insertKeyLocally,
    deleteKeyLocally,
    deleteKeysLocally
  }
}
