import { apiClient } from '../client'

export interface ConnectionTraceDetail {
  version: number
  trace_id: string
  started_at: string
  total_ms: number
  status: number
  canceled: boolean
  outcome?: string
  terminal?: string
  client_disconnect: boolean
  downstream_error: boolean
  downstream_bytes: number
  attempts: Array<Record<string, unknown>>
  events: Record<string, number>
  spans: Array<Record<string, unknown>>
}

export interface ConnectionTrace {
  trace_id: string
  started_at: string
  request_id: string
  client_request_id: string
  user_id: number
  api_key_id: number
  account_id: number
  group_id: number
  model: string
  path: string
  client_ip: string
  user_agent: string
  stream: boolean
  state: string
  source: string
  host: string
  scheme: string
  method: string
  http_version: string
  tls_version: string
  upstream_status: string
  network_error: string
  legacy: boolean
  detail: ConnectionTraceDetail
  user_name?: string
  user_email?: string
  api_key_name?: string
  account_name?: string
  group_name?: string
  error_type?: string
  error_status?: number
}

export interface ConnectionTraceQuery {
  page?: number
  page_size?: number
  state?: string
  source?: string
  model?: string
  request_id?: string
  client_ip?: string
  host?: string
  start_time?: string
  end_time?: string
}

export interface ConnectionTraceList {
  items: ConnectionTrace[]
  has_more: boolean
  page: number
  page_size: number
  retention_days: number
}

export interface ConnectionDiagnosticsSettings {
  enabled: boolean
  retention_days: number
  body_capture: false
}

export async function list(params: ConnectionTraceQuery): Promise<ConnectionTraceList> {
  const { data } = await apiClient.get('/admin/usage/connections', { params })
  return data
}

export async function settings(): Promise<ConnectionDiagnosticsSettings> {
  const { data } = await apiClient.get('/admin/usage/connection-diagnostics')
  return data
}

export async function setEnabled(enabled: boolean): Promise<ConnectionDiagnosticsSettings> {
  const { data } = await apiClient.put('/admin/usage/connection-diagnostics', { enabled })
  return data
}

export default { list, settings, setEnabled }
