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
import { useTranslation } from 'react-i18next'

import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getTodayUsage } from '@/features/dashboard/api'
import {
  calculateOfficialCost,
  formatOfficialCNY,
} from '@/features/dashboard/lib'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'
import { Wallet } from 'lucide-react'

/** 概览页的官方 API 计费估算(个人口径,今天 0 点起) */
export function OfficialCostSelfPanel() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const username = user?.username

  const query = useQuery({
    queryKey: ['dashboard', 'overview', 'official-cost-self'],
    queryFn: async () => requireServerSuccess(await getTodayUsage()),
    staleTime: 60 * 1000,
    retry: false,
  })

  const items = (query.data?.data ?? []).filter(
    (item) => item.username === username
  )
  const totalCNY = calculateOfficialCost(items).totalCNY
  const hasData = items.some(
    (item) =>
      (Number(item.prompt_tokens) || 0) + (Number(item.completion_tokens) || 0) >
      0
  )

  return (
    <div className='flex items-center justify-between gap-2'>
      <div className='flex items-center gap-1.5'>
        <IconBadge tone='warning' size='xs'>
          <Wallet />
        </IconBadge>
        <span className='text-muted-foreground text-[11px] font-medium'>
          {t('Official API cost estimate')}
        </span>
      </div>
      {query.isLoading ? (
        <Skeleton className='h-4 w-16' />
      ) : (
        <span
          className='font-mono text-xs font-bold tabular-nums'
          title={`${totalCNY.toFixed(6)} CNY`}
        >
          {hasData ? formatOfficialCNY(totalCNY) : '--'}
        </span>
      )}
    </div>
  )
}
