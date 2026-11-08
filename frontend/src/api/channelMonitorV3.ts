import { apiClient } from './client'
import { repeatedArrayParamsSerializer } from './channelMonitorV2'
import type { MonitorFilter, MonitorMatrixGroupBy, MonitorMatrixResponse, MonitorSnapshot } from './channelMonitorV2'

/**
 * Channel Monitor V3 API.
 *
 * V3 is a standalone product, not a V1/V2 mode (ADR 0008): its board and its
 * settings live on their own endpoints and are gated only by
 * `channel_monitor_v3_config.enabled`. Nothing here reads `channel_monitor_enabled`
 * or `channel_monitor_mode`.
 */

export interface ChannelMonitorV3ProbeConfig {
  endpoint: string
  provider: string
  primary_model: string
  extra_models: string[]
  interval_seconds: number
  /** Ciphertext is never returned by the API; this key only exists on writes. */
  api_key_encrypted?: string
}

/** Cache-window units accepted by the backend. */
export type ChannelMonitorV3WindowUnit = 'minute' | 'day'

export interface ChannelMonitorV3GroupConfig {
  group_id: number
  enabled: boolean
  models: string[]
  probe_enabled: boolean
  /**
   * Per-group probe identity. An empty endpoint + primary_model means "inherit
   * the global probe": every group used to share one key, but each group holds
   * different models and therefore needs its own key.
   */
  probe?: ChannelMonitorV3ProbeConfig
  /** Derived: this group has its own stored key. The key itself is never sent. */
  api_key_set?: boolean
  /** Derived: this group still inherits the global probe identity. */
  probe_inherited?: boolean
}

export interface ChannelMonitorV3Config {
  version: number
  /** V3 master switch. It controls V3 only. */
  enabled: boolean
  groups: ChannelMonitorV3GroupConfig[]
  probe: ChannelMonitorV3ProbeConfig
  /**
   * Look-back window for the real-traffic cache average (ADR 0009).
   * `cache_average_window_value` + `cache_average_window_unit` is authoritative;
   * the legacy days field is kept for old clients and mirrors whole days.
   */
  cache_average_window_days: number
  cache_average_window_value?: number
  cache_average_window_unit?: ChannelMonitorV3WindowUnit
  updated_at?: string
  updated_by?: number
  /** Derived flag: whether a probe key is stored. The key itself is never sent. */
  api_key_set?: boolean
}

export interface ChannelMonitorV3ConfigPayload extends ChannelMonitorV3Config {
  /** Plain key on write; empty means "keep the stored key". */
  api_key?: string
  /** Per-group plain keys keyed by group id; empty/missing means "keep stored". */
  group_api_keys?: Record<string, string>
}

export interface ChannelMonitorV3MonitorableGroup {
  id: number
  name: string
  platform: string
  status: string
}

export async function getV3Config() {
  const { data } = await apiClient.get<ChannelMonitorV3Config>('/admin/channel-monitor-v3/config')
  return data
}

export async function updateV3Config(config: ChannelMonitorV3ConfigPayload) {
  const { data } = await apiClient.put<ChannelMonitorV3Config>('/admin/channel-monitor-v3/config', config)
  return data
}

/** Candidate groups. Carries no allow-list / pricing gate: V3 only needs a checkbox. */
export async function listV3Groups() {
  const { data } = await apiClient.get<ChannelMonitorV3MonitorableGroup[]>('/admin/channel-monitor-v3/groups')
  return data
}

export async function probeV3Now() {
  const { data } = await apiClient.post<{ ok: boolean }>('/admin/channel-monitor-v3/probe')
  return data
}

/** Board availability. The only thing that decides this is V3's own switch. */
export async function getV3Status() {
  const { data } = await apiClient.get<{ enabled: boolean }>('/channel-monitor-v3/status')
  return data
}

const v3RequestConfig = (
  filter: MonitorFilter,
  signal?: AbortSignal,
  extraParams: Record<string, unknown> = {},
) => ({
  params: {
    range: filter.range,
    platform: filter.platforms,
    group_id: filter.groupIds,
    model: filter.models,
    ...extraParams,
  },
  paramsSerializer: { serialize: repeatedArrayParamsSerializer },
  signal,
})

export async function getV3Snapshot(filter: MonitorFilter, admin = false, signal?: AbortSignal) {
  const base = admin ? '/admin/channel-monitor-v3' : '/channel-monitor-v3'
  const { data } = await apiClient.get<MonitorSnapshot>(`${base}/snapshot`, v3RequestConfig(filter, signal))
  return data
}

export async function getV3Matrix(
  filter: MonitorFilter,
  groupBy: MonitorMatrixGroupBy,
  admin = false,
  signal?: AbortSignal,
) {
  const base = admin ? '/admin/channel-monitor-v3' : '/channel-monitor-v3'
  const { data } = await apiClient.get<MonitorMatrixResponse>(
    `${base}/matrix`,
    v3RequestConfig(filter, signal, { group_by: groupBy }),
  )
  return data
}
