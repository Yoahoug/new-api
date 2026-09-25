/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'

import type {
  FlowQuotaDataItem,
  QuotaDataItem,
  UptimeGroupResult,
} from './types'
import type { OfficialPriceConfigItem } from './lib/official-prices'

// ============================================================================
// Dashboard APIs
// ============================================================================

// ----------------------------------------------------------------------------
// Quota & Usage Data
// ----------------------------------------------------------------------------

// Get user quota data within a time range
// Admin users get all users' data by default.
export async function getUserQuotaDates(
  params: {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  isAdmin = false
) {
  const endpoint = isAdmin ? '/api/data' : '/api/data/self'
  const res = await api.get<{ success: boolean; data: QuotaDataItem[] }>(
    endpoint,
    { params }
  )
  return res.data
}

// Get per-user × per-model usage aggregated since local midnight (today).
// Admin without `username` param returns all users; regular users are
// server-side restricted to their own row.
export interface TodayUsageItem {
  username: string
  model_name: string
  quota: number
  prompt_tokens: number
  completion_tokens: number
  cache_tokens: number
  count: number
}

export interface DailyUsageItem extends TodayUsageItem {
  day: string
}

export async function getTodayUsage() {
  const res = await api.get<{
    success: boolean
    data: TodayUsageItem[]
  }>('/api/data/today')
  return res.data
}

// Daily per-user × per-model usage for the last `days` days (default 7,
// server-capped at 90), including token details for official cost estimates.
// Admin may pass username=all for site-wide rows.
// Get the admin-maintained official list-price configuration. Entries not
// configured fall back to the built-in table in the frontend.
export async function getOfficialPrices() {
  const res = await api.get<{
    success: boolean
    data: OfficialPriceConfigItem[]
  }>('/api/official_prices')
  return res.data
}

export async function getDailyUsage(params?: { days?: number; username?: string }) {
  const res = await api.get<{
    success: boolean
    data: DailyUsageItem[]
  }>('/api/data/daily', {
    params: {
      days: params?.days,
      username: params?.username,
    },
  })
  return res.data
}

// ----------------------------------------------------------------------------
// System Monitoring
// ----------------------------------------------------------------------------

export async function getUserQuotaDataByUsers(params: {
  start_timestamp: number
  end_timestamp: number
}) {
  const res = await api.get<{ success: boolean; data: QuotaDataItem[] }>(
    '/api/data/users',
    { params }
  )
  return res.data
}

export async function getFlowQuotaDates(
  params: {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  isAdmin = false
) {
  const endpoint = isAdmin ? '/api/data/flow' : '/api/data/flow/self'
  const res = await api.get<{
    success: boolean
    data?: FlowQuotaDataItem[]
    message?: string
  }>(endpoint, { params })
  return res.data
}

// Get uptime monitoring status for all services
export async function getUptimeStatus() {
  const res = await api.get<{ success: boolean; data: UptimeGroupResult[] }>(
    '/api/uptime/status'
  )
  return res.data
}
