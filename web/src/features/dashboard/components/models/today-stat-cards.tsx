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
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  getTodayUsage,
  type TodayUsageItem,
} from '@/features/dashboard/api'
import { useOfficialPrices } from '@/features/dashboard/hooks/use-official-prices'
import {
  calculateOfficialCost,
  formatOfficialCNY,
} from '@/features/dashboard/lib'
import { getCurrencyDisplay } from '@/lib/currency'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'
import {
  Activity,
  ChevronDown,
  Coins,
  Database,
  Layers,
  Wallet,
  Zap,
} from 'lucide-react'

interface TodayStats {
  count: number
  quota: number
  tokens: number
  cacheRate: number | null
}

function aggregateTodayStats(items: TodayUsageItem[]): TodayStats {
  let count = 0
  let quota = 0
  let promptTokens = 0
  let completionTokens = 0
  let cacheTokens = 0
  for (const item of items) {
    count += Number(item.count) || 0
    quota += Number(item.quota) || 0
    promptTokens += Number(item.prompt_tokens) || 0
    completionTokens += Number(item.completion_tokens) || 0
    cacheTokens += Number(item.cache_tokens) || 0
  }
  const cacheRate =
    promptTokens > 0 ? Math.min(cacheTokens / promptTokens, 1) : null
  return { count, quota, tokens: promptTokens + completionTokens, cacheRate }
}

function formatCompact(value: number): string {
  return new Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumFractionDigits: 2,
  }).format(value)
}

export function TodayStatCards() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = !!(user?.role && user.role >= 10)
  const [items, setItems] = useState<TodayUsageItem[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [costOpen, setCostOpen] = useState(false)

  useEffect(() => {
    const abortController = new AbortController()
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)

    void getTodayUsage()
      .then((res) => {
        if (abortController.signal.aborted) return
        setItems(res?.data || [])
      })
      .catch(() => {
        if (abortController.signal.aborted) return
        setItems(null)
      })
      .finally(() => {
        if (!abortController.signal.aborted) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
    }
  }, [])

  const stats = useMemo(() => aggregateTodayStats(items ?? []), [items])

  // 官方牌价估算与今日用量同源同口径(今天 0 点起)
  const officialPrices = useOfficialPrices()
  const officialCost = useMemo(
    () => calculateOfficialCost(items ?? [], officialPrices),
    [items, officialPrices]
  )
  const hasCost = officialCost.vendors.length > 0

  const timeRange = computeTimeRange(1, undefined, undefined, true)
  const todayMinutes = Math.max(
    1,
    Math.floor((Date.now() / 1000 - timeRange.start_timestamp) / 60)
  )
  const { config: currencyConfig } = getCurrencyDisplay()

  const values: Record<string, string> = {
    todayCount: stats.count.toLocaleString(),
    todayQuota: new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'CNY',
      currencyDisplay: 'narrowSymbol',
      maximumFractionDigits: 4,
    }).format(stats.quota / currencyConfig.quotaPerUnit),
    todayTokens: formatCompact(stats.tokens),
    todayCacheRate:
      stats.cacheRate !== null
        ? `${(stats.cacheRate * 100).toFixed(2)}%`
        : '--',
    todayRpm: (stats.count / todayMinutes).toFixed(2),
  }

  const cards = [
    { key: 'todayCount', title: t('Today requests'), icon: Activity, iconTone: 'info' as const },
    { key: 'todayQuota', title: t('Today consumption'), icon: Coins, iconTone: 'success' as const },
    { key: 'todayTokens', title: t('Today tokens'), icon: Layers, iconTone: 'chart-4' as const },
    { key: 'todayCacheRate', title: t('Cache hit rate'), icon: Database, iconTone: 'chart-2' as const },
    { key: 'todayRpm', title: t('Average RPM (today)'), icon: Zap, iconTone: 'warning' as const },
  ]

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='bg-muted/30 text-muted-foreground flex items-center gap-2 px-2.5 py-1.5 sm:px-5 sm:py-2'>
        <span className='text-xs font-semibold'>{t('Usage today')}</span>
        <span className='text-muted-foreground/60 text-[11px]'>
          {isAdmin ? t('Site-wide') : t('Personal')}
        </span>
      </div>
      <div className='divide-border/60 grid min-w-0 grid-cols-2 divide-x sm:grid-cols-3 lg:grid-cols-5'>
        {cards.map((card, idx) => {
          const Icon = card.icon
          let content
          if (loading) {
            content = (
              <Skeleton className='mt-1 h-5 w-16 sm:mt-2 sm:h-7 sm:w-20' />
            )
          } else {
            content = (
              <div
                className='text-foreground mt-1 max-w-full truncate font-mono text-base leading-tight font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl sm:leading-normal'
                title={values[card.key]}
              >
                {values[card.key]}
              </div>
            )
          }
          return (
            <div
              key={card.key}
              className={cn(
                'min-w-0 px-2.5 py-1.5 sm:px-5 sm:py-4',
                idx === cards.length - 1 &&
                  cards.length % 2 !== 0 &&
                  'col-span-2 sm:col-span-1'
              )}
            >
              <div className='flex h-4 min-w-0 items-center gap-1.5 sm:h-7 sm:gap-2'>
                <IconBadge
                  tone={card.iconTone}
                  size='stat'
                  className='size-4 rounded-sm sm:size-7 sm:rounded-md [&>svg]:size-2.5 sm:[&>svg]:size-3.5'
                >
                  <Icon />
                </IconBadge>
                <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium tracking-wide uppercase sm:text-xs sm:tracking-wider'>
                  {card.title}
                </div>
              </div>
              {content}
            </div>
          )
        })}
      </div>
      {hasCost && (
        <Collapsible open={costOpen} onOpenChange={setCostOpen}>
          <CollapsibleTrigger
            render={
              <button
                type='button'
                className='bg-muted/30 hover:bg-muted/50 text-muted-foreground flex w-full items-center gap-2 border-t px-2.5 py-1.5 text-left transition-colors sm:px-5 sm:py-2'
                aria-expanded={costOpen}
              />
            }
          >
            <IconBadge
              tone='warning'
              size='stat'
              className='size-4 rounded-sm sm:size-5 sm:rounded-md [&>svg]:size-2.5 sm:[&>svg]:size-3'
            >
              <Wallet />
            </IconBadge>
            <span className='text-muted-foreground truncate text-[11px] font-medium tracking-wide uppercase sm:text-xs sm:tracking-wider'>
              {t('Official API cost estimate')}
            </span>
            {loading ? (
              <Skeleton className='h-4 w-14' />
            ) : (
              <span
                className='text-foreground ml-auto font-mono text-sm font-bold tabular-nums'
                title={`${officialCost.totalCNY.toFixed(6)} CNY`}
              >
                {formatOfficialCNY(officialCost.totalCNY)}
              </span>
            )}
            <ChevronDown
              className={cn(
                'text-muted-foreground size-3.5 shrink-0 transition-transform',
                costOpen && 'rotate-180'
              )}
              aria-hidden='true'
            />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className='divide-border/60 grid grid-cols-2 divide-x border-t sm:grid-cols-3 lg:grid-cols-5'>
              {officialCost.vendors.map((v) => (
                <div key={v.vendor} className='min-w-0 px-2.5 py-1.5 sm:px-5 sm:py-3'>
                  <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium tracking-wide uppercase sm:text-xs sm:tracking-wider'>
                    {v.label}
                  </div>
                  <div
                    className='text-foreground mt-0.5 max-w-full truncate font-mono text-sm leading-tight font-bold tabular-nums sm:text-base'
                    title={`${v.totalCNY.toFixed(6)} CNY · ${v.models.join(', ')}`}
                  >
                    {formatOfficialCNY(v.totalCNY)}
                  </div>
                  <div className='text-muted-foreground/60 mt-0.5 truncate text-[11px]'>
                    {v.models.join(', ')}
                  </div>
                </div>
              ))}
              {officialCost.unmatchedModels.length > 0 && (
                <div className='text-muted-foreground/60 col-span-2 px-2.5 py-1.5 text-[11px] sm:col-span-3 sm:px-5 lg:col-span-5'>
                  {t('No list price')}: {officialCost.unmatchedModels.join(', ')}
                </div>
              )}
            </div>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  )
}
