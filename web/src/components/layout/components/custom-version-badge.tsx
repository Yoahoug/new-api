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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Skeleton } from '@/components/ui/skeleton'
import { useStatus } from '@/hooks/use-status'
import { cn } from '@/lib/utils'

import { splitCustomVersion } from '@/features/system-update/releases'

type CustomVersionBadgeProps = {
  className?: string
}

/**
 * Version pill for custom (fork) builds in the top bar.
 * Shows the full custom version and, in a popover, the upstream base the
 * fork is built on. Deliberately independent from the upstream update
 * checker: custom builds are numbered against the fork's base release,
 * not upstream tags.
 */
export function CustomVersionBadge(props: CustomVersionBadgeProps) {
  const { t } = useTranslation()
  const { status, loading } = useStatus()
  const version = status?.version?.trim()
  const custom = version ? splitCustomVersion(version) : null

  if (loading) {
    return <Skeleton className='h-5 w-28 rounded-md' />
  }
  if (!version) return null

  return (
    <Popover>
      <PopoverTrigger
        render={
          <button
            type='button'
            className={cn(
              'text-muted-foreground hover:bg-accent hover:text-foreground inline-flex h-7 max-w-40 items-center gap-1.5 rounded-md px-1.5 text-xs transition-colors outline-none select-none',
              props.className
            )}
            title={version}
          />
        }
      >
        <span className='truncate font-mono'>{version}</span>
        {custom && (
          <Badge variant='secondary' className='h-4 shrink-0 px-1 text-[10px]'>
            {t('Custom build')}
          </Badge>
        )}
      </PopoverTrigger>
      <PopoverContent align='start' className='w-64'>
        <div className='space-y-2 text-sm'>
          <div className='flex items-center justify-between gap-2'>
            <span className='text-muted-foreground text-xs'>
              {t('Current version')}
            </span>
            <span className='font-mono text-xs font-semibold'>{version}</span>
          </div>
          {custom && (
            <div className='flex items-center justify-between gap-2'>
              <span className='text-muted-foreground text-xs'>
                {t('Upstream base')}
              </span>
              <span className='font-mono text-xs font-semibold'>
                {custom.baseVersion}
              </span>
            </div>
          )}
          {custom && (
            <div className='flex items-center justify-between gap-2'>
              <span className='text-muted-foreground text-xs'>
                {t('Custom build number')}
              </span>
              <span className='font-mono text-xs font-semibold'>
                #{custom.buildNumber}
              </span>
            </div>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}
