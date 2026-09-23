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
import { CalendarDays, Flame, Wallet } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Area,
  AreaChart,
  CartesianGrid,
  XAxis,
  YAxis,
} from 'recharts'

import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart'
import { IconBadge } from '@/components/ui/icon-badge'
import { getDailyUsage } from '@/features/dashboard/api'
import {
  calculateOfficialCost,
  formatOfficialCNY,
} from '@/features/dashboard/lib'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { PanelWrapper } from '../ui/panel-wrapper'

const CHART_CONFIG = {
  cost: { label: 'Official API cost estimate' },
} satisfies Parameters<typeof ChartContainer>[0]['config']

interface DailyCostPoint {
  day: string
  label: string
  cost: number
}

/** 按天汇总官方牌价估算(人民币),缺失的天补 0,保证 7 天连续横轴 */
function buildDailySeries(
  rows: Array<{
    day: string
    model_name: string
    prompt_tokens: number
    completion_tokens: number
    cache_tokens: number
  }>,
  days: number
): DailyCostPoint[] {
  const byDay = new Map<string, number>()
  for (const row of rows) {
    const cost = calculateOfficialCost([row]).totalCNY
    if (cost > 0) {
      byDay.set(row.day, (byDay.get(row.day) ?? 0) + cost)
    }
  }
  const series: DailyCostPoint[] = []
  const formatter = new Intl.DateTimeFormat(undefined, {
    month: 'numeric',
    day: 'numeric',
  })
  const today = new Date()
  for (let offset = days - 1; offset >= 0; offset--) {
    const date = new Date(today)
    date.setDate(today.getDate() - offset)
    const month = String(date.getMonth() + 1).padStart(2, '0')
    const dateDay = String(date.getDate()).padStart(2, '0')
    const day = `${date.getFullYear()}-${month}-${dateDay}`
    series.push({
      day,
      label: formatter.format(date),
      cost: byDay.get(day) ?? 0,
    })
  }
  return series
}

/** 概览页独立的官方 API 计费估算卡片:今日估算 + 最近 7 天趋势图 */
export function OfficialCostCard() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)

  const query = useQuery({
    queryKey: ['dashboard', 'overview', 'official-cost-daily', 7],
    queryFn: async () => requireServerSuccess(await getDailyUsage({ days: 7 })),
    staleTime: 60 * 1000,
    retry: false,
  })

  // 概览页是个人视角:即使接口意外返回全站行,也只取自己的
  const myRows = useMemo(
    () =>
      (query.data?.data ?? []).filter(
        (row) => row.username === user?.username
      ),
    [query.data, user?.username]
  )
  const series = useMemo(() => buildDailySeries(myRows, 7), [myRows])
  const totalCNY = series.reduce((sum, point) => sum + point.cost, 0)
      const todayCost = series.at(-1)?.cost ?? 0
      let todayCostDisplay = '--'
      if (query.isLoading) {
        todayCostDisplay = ''
      } else if (todayCost > 0) {
        todayCostDisplay = formatOfficialCNY(todayCost)
      }

  return (
    <PanelWrapper
      title={
        <span className='flex items-center gap-2'>
          <IconBadge tone='warning' size='sm'>
            <Wallet />
          </IconBadge>
          {t('Official API cost estimate')}
        </span>
      }
      description={t('Estimated at official list prices (CNY)')}
      height='h-full'
      contentClassName='p-0'
      loading={query.isLoading}
      empty={!query.isLoading && totalCNY <= 0}
      emptyMessage={t('No data available')}
    >
      <div className='flex h-full flex-col gap-3 px-3 pb-3 pt-2.5 sm:px-4 sm:pb-4 sm:pt-3'>
        <div className='grid grid-cols-2 gap-2 sm:gap-3'>
          <div className='bg-muted/40 flex min-h-20 flex-col justify-center rounded-xl border px-3 py-3 sm:min-h-24 sm:px-4 sm:py-4'>
            <div className='text-muted-foreground flex items-center gap-1 text-[11px] leading-none font-medium'>
              <Flame className='size-3 shrink-0' aria-hidden='true' />
              <span className='truncate'>{t('Today usage')}</span>
            </div>
            <div
              className='text-foreground mt-2 truncate font-mono text-lg font-bold tabular-nums sm:text-xl'
              title={`${todayCost.toFixed(6)} CNY`}
            >
              {todayCostDisplay}
            </div>
          </div>
          <div className='bg-muted/40 flex min-h-20 flex-col justify-center rounded-xl border px-3 py-3 sm:min-h-24 sm:px-4 sm:py-4'>
            <div className='text-muted-foreground flex items-center gap-1 text-[11px] leading-none font-medium'>
              <CalendarDays className='size-3 shrink-0' aria-hidden='true' />
              <span className='truncate'>{t('Last 7 days total')}</span>
            </div>
            <div
              className='text-foreground mt-2 truncate font-mono text-lg font-bold tabular-nums sm:text-xl'
              title={`${totalCNY.toFixed(6)} CNY`}
            >
              {totalCNY > 0 ? formatOfficialCNY(totalCNY) : '--'}
            </div>
          </div>
        </div>
        <div className='min-h-0 flex-1'>
          <ChartContainer config={CHART_CONFIG} className='h-full w-full'>
            <AreaChart
              data={series}
              margin={{ top: 8, right: 20, left: 0, bottom: 0 }}
            >
              <defs>
                <linearGradient
                  id='officialCostFill'
                  x1='0'
                  y1='0'
                  x2='0'
                  y2='1'
                >
                  <stop
                    offset='0%'
                    stopColor='var(--chart-2)'
                    stopOpacity={0.35}
                  />
                  <stop
                    offset='100%'
                    stopColor='var(--chart-2)'
                    stopOpacity={0.02}
                  />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} strokeDasharray='3 3' />
              <XAxis
                dataKey='label'
                tickLine={false}
                axisLine={false}
                tickMargin={6}
                interval={0}
                tick={{ fontSize: 10 }}
              />
              <YAxis
                width={44}
                tickLine={false}
                axisLine={false}
                tick={{ fontSize: 10 }}
                tickFormatter={(value: number) =>
                  `¥${value >= 100 ? Math.round(value) : value.toFixed(value >= 1 ? 1 : 2)}`
                }
              />
              <ChartTooltip
                cursor={{ stroke: 'var(--border)' }}
                content={
                  <ChartTooltipContent
                    labelKey='label'
                    nameKey='cost'
                    formatter={(value) => formatOfficialCNY(Number(value))}
                  />
                }
              />
              <Area
                type='monotone'
                dataKey='cost'
                stroke='var(--chart-2)'
                strokeWidth={2}
                fill='url(#officialCostFill)'
              />
            </AreaChart>
          </ChartContainer>
        </div>
      </div>
    </PanelWrapper>
  )
}
