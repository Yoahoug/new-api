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
import { useQuery } from '@tanstack/react-query'

import { getTodayUsage } from '@/features/dashboard/api'
import { calculateOfficialCost } from '@/features/dashboard/lib'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface TodayUsageByUser {
  /** username -> 今日消耗 quota */
  quota: Map<string, number>
  /** username -> 官方牌价估算(人民币元) */
  officialCNY: Map<string, number>
}

/** 用户列表"今日消耗"列数据:quota 与官方 API 估价按 username 聚合 */
export function useTodayQuotaByUser() {
  const query = useQuery({
    queryKey: ['users', 'today-usage'],
    queryFn: async () => requireServerSuccess(await getTodayUsage()),
    select: (res): TodayUsageByUser => {
      // /api/data/today 按 用户×模型 分组,同一用户会有多行,需累加
      const quota = new Map<string, number>()
      const rowsByUser = new Map<string, typeof res.data>()
      for (const item of res.data || []) {
        quota.set(
          item.username,
          (quota.get(item.username) ?? 0) + (Number(item.quota) || 0)
        )
        const rows = rowsByUser.get(item.username) ?? []
        rows.push(item)
        rowsByUser.set(item.username, rows)
      }
      const officialCNY = new Map<string, number>()
      for (const [username, rows] of rowsByUser) {
        officialCNY.set(username, calculateOfficialCost(rows).totalCNY)
      }
      return { quota, officialCNY }
    },
    staleTime: 60_000,
    retry: false,
  })
  return query
}
