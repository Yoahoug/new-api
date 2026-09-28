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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { handleServerError } from '@/lib/handle-server-error'
import { resolveLocalizedText } from '@/lib/localized-text'

import { listRequestGuards, updateRequestGuardConfig } from '../api'
import type { RequestGuardConfigField, RequestGuardItem } from '../types'
import { PluginIcon } from './plugin-icon'

/**
 * Renders one declared config field. Only the scalar types get direct inputs;
 * array and object fields are edited as JSON, which keeps the form honest
 * about what the plugin will actually receive.
 */
function ConfigFieldInput(props: {
  field: RequestGuardConfigField
  value: unknown
  language: string
  onChange: (value: unknown) => void
}) {
  const { t } = useTranslation()
  const { field, value, onChange } = props
  const label = resolveLocalizedText(field.description, props.language) || field.name
  switch (field.type) {
    case 'boolean':
      return (
        <label className='flex items-center justify-between gap-3 text-sm'>
          <span>{label}</span>
          <Switch
            checked={value === true}
            onCheckedChange={(checked) => onChange(checked)}
          />
        </label>
      )
    case 'integer':
    case 'number':
      return (
        <label className='block space-y-1 text-sm'>
          <span>{label}</span>
          <Input
            type='number'
            step={field.type === 'integer' ? 1 : 'any'}
            value={typeof value === 'number' ? String(value) : ''}
            onChange={(event) => {
              const parsed = Number(event.target.value)
              if (event.target.value !== '' && Number.isFinite(parsed)) {
                onChange(field.type === 'integer' ? Math.trunc(parsed) : parsed)
              }
            }}
          />
        </label>
      )
    case 'enum': {
      const options = field.enumValues ?? []
      return (
        <label className='block space-y-1 text-sm'>
          <span>{label}</span>
          <select
            className='border-input bg-background flex h-9 w-full rounded-md px-3 text-sm'
            value={typeof value === 'string' ? value : ''}
            onChange={(event) => onChange(event.target.value)}
          >
            <option value=''>{t('Not set')}</option>
            {options.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
        </label>
      )
    }
    default:
      return (
        <label className='block space-y-1 text-sm'>
          <span>{label}</span>
          <textarea
            className='border-input bg-background min-h-20 w-full rounded-md px-3 py-2 font-mono text-xs'
            spellCheck={false}
            value={
              value === undefined || value === null
                ? ''
                : JSON.stringify(value, null, 2)
            }
            onChange={(event) => {
              const raw = event.target.value
              if (raw.trim() === '') {
                onChange(undefined)
                return
              }
              try {
                onChange(JSON.parse(raw))
              } catch {
                /* Keep the previous value until the JSON parses again. */
              }
            }}
          />
        </label>
      )
  }
}

function GuardCard(props: { guard: RequestGuardItem }) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const guard = props.guard
  const [draft, setDraft] = useState<Record<string, unknown>>(
    () => guard.config ?? {}
  )
  const saveMutation = useMutation({
    mutationFn: (config: Record<string, unknown>) =>
      updateRequestGuardConfig(guard.key, config),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['request-guards'] })
      toast.success(t('Guard configuration saved'))
    },
    onError: (error) => handleServerError(error),
  })
  const fields = guard.guard.configFields ?? []
  const scope = [
    guard.guard.methods?.length
      ? guard.guard.methods.join(', ')
      : t('All methods'),
    guard.guard.paths?.length
      ? guard.guard.paths.join(', ')
      : t('All paths'),
    guard.guard.groups?.length
      ? t('Groups: {{groups}}', { groups: guard.guard.groups.join(', ') })
      : t('All groups'),
    guard.guard.models?.length
      ? t('Models: {{models}}', { models: guard.guard.models.join(', ') })
      : t('All models'),
  ]

  return (
    <Card>
      <CardHeader className='flex flex-row items-center gap-3 space-y-0'>
        <PluginIcon
          plugin={{
            key: guard.key,
            name: guard.name,
            icon: guard.icon,
            hasIcon: guard.hasIcon,
          }}
          size={28}
        />
        <div className='min-w-0 flex-1'>
          <CardTitle className='truncate text-base'>{guard.name}</CardTitle>
          <p className='text-muted-foreground truncate font-mono text-xs'>
            {guard.key} · v{guard.version}
          </p>
        </div>
        <div className='flex shrink-0 items-center gap-1.5'>
          <Badge variant='outline'>
            {t('Priority {{priority}}', { priority: guard.guard.priority })}
          </Badge>
          {guard.guard.exclusive && <Badge>{t('Exclusive')}</Badge>}
          {guard.guard.failOpen && (
            <Badge variant='secondary'>{t('Fail open')}</Badge>
          )}
        </div>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {resolveLocalizedText(guard.description, i18n.language)}
        </p>
        <p className='text-muted-foreground text-xs'>
          {scope.join(' · ')}
          {guard.guard.timeoutMs
            ? ` · ${t('Timeout {{ms}} ms', { ms: guard.guard.timeoutMs })}`
            : ''}
        </p>
        {fields.length > 0 && (
          <div className='space-y-3 border-t pt-3'>
            {fields.map((field) => (
              <ConfigFieldInput
                key={field.name}
                field={field}
                language={i18n.language}
                value={draft[field.name]}
                onChange={(value) =>
                  setDraft((previous) => {
                    const next = { ...previous }
                    if (value === undefined) {
                      delete next[field.name]
                    } else {
                      next[field.name] = value
                    }
                    return next
                  })
                }
              />
            ))}
            <div className='flex justify-end gap-2'>
              <Button
                variant='outline'
                size='sm'
                onClick={() => setDraft(guard.config ?? {})}
                disabled={saveMutation.isPending}
              >
                {t('Reset')}
              </Button>
              <Button
                size='sm'
                onClick={() => saveMutation.mutate(draft)}
                disabled={saveMutation.isPending}
              >
                {t('Save configuration')}
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export function RequestGuardsPanel() {
  const { t } = useTranslation()
  const guardsQuery = useQuery({
    queryKey: ['request-guards'],
    queryFn: listRequestGuards,
  })
  const guards = guardsQuery.data ?? []
  return (
    <div className='space-y-3'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Request guards run in listed order before a channel is selected. A denied request never reaches an upstream and never reserves quota.'
        )}
      </p>
      {guardsQuery.isLoading ? (
        <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
      ) : guards.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No request guard plugins are installed.')}
        </p>
      ) : (
        guards.map((guard) => <GuardCard key={guard.key} guard={guard} />)
      )}
    </div>
  )
}
