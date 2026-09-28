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
import { ShieldOff } from 'lucide-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'


import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import { resolveLocalizedText } from '@/lib/localized-text'

import { listRequestGuards, updateRequestGuardConfig } from '../api'
import type { RequestGuardConfigField, RequestGuardItem } from '../types'
import { PluginIcon } from './plugin-icon'

/**
 * JSON editor for array/object config fields. The raw text lives in local
 * state so every keystroke sticks while the JSON is still incomplete; only a
 * parseable value reaches the draft. Remounting (reset) re-reads the saved
 * value.
 */
function JsonConfigField(props: {
  id: string
  label: string
  name: string
  value: unknown
  onChange: (value: unknown) => void
}) {
  const [text, setText] = useState(() =>
    props.value === undefined || props.value === null
      ? ''
      : JSON.stringify(props.value, null, 2)
  )
  return (
    <Field className='sm:col-span-2'>
      <FieldLabel htmlFor={props.id}>{props.label}</FieldLabel>
      <FieldDescription className='font-mono text-xs'>
        {props.name}
      </FieldDescription>
      <Textarea
        id={props.id}
        className='max-h-64 min-h-16 font-mono text-xs'
        spellCheck={false}
        value={text}
        onChange={(event) => {
          const raw = event.target.value
          setText(raw)
          if (raw.trim() === '') {
            props.onChange(undefined)
            return
          }
          try {
            props.onChange(JSON.parse(raw))
          } catch {
            /* Keep the last valid draft until the JSON parses again. */
          }
        }}
      />
    </Field>
  )
}


/**
 * Renders one declared config field. Scalar types get direct inputs; array
 * and object fields are edited as JSON across the full form width, which
 * keeps the form honest about what the plugin will actually receive.
 */
function ConfigFieldInput(props: {
  field: RequestGuardConfigField
  value: unknown
  language: string
  onChange: (value: unknown) => void
}) {
  const { t } = useTranslation()
  const inputId = useId()
  const { field, value, onChange } = props
  const label =
    resolveLocalizedText(field.description, props.language) || field.name
  const nameHint = (
    <FieldDescription className='font-mono text-xs'>
      {field.name}
    </FieldDescription>
  )
  switch (field.type) {
    case 'boolean':
      return (
        <Field orientation='horizontal'>
          <FieldContent>
            <FieldLabel>{label}</FieldLabel>
            {nameHint}
          </FieldContent>
          <Switch
            checked={value === true}
            onCheckedChange={(checked) => onChange(checked)}
            aria-label={label}
          />
        </Field>
      )
    case 'integer':
    case 'number':
      return (
        <Field>
          <FieldLabel htmlFor={inputId}>{label}</FieldLabel>
          {nameHint}
          <Input
            id={inputId}
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
        </Field>
      )
    case 'enum': {
      const options = field.enumValues ?? []
      return (
        <Field>
          <FieldLabel htmlFor={inputId}>{label}</FieldLabel>
          {nameHint}
          <NativeSelect
            id={inputId}
            value={typeof value === 'string' ? value : ''}
            onChange={(event) => onChange(event.target.value)}
          >
            <NativeSelectOption value=''>{t('Not set')}</NativeSelectOption>
            {options.map((option) => (
              <NativeSelectOption key={option} value={option}>
                {option}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
      )
    }
    case 'array':
    case 'object':
      return (
        <JsonConfigField
          id={inputId}
          label={label}
          name={field.name}
          value={value}
          onChange={onChange}
        />
      )
    default:
      return (
        <Field>
          <FieldLabel htmlFor={inputId}>{label}</FieldLabel>
          {nameHint}
          <Input
            id={inputId}
            type='text'
            value={
              value === undefined || value === null ? '' : String(value)
            }
            onChange={(event) => onChange(event.target.value)}
          />
        </Field>
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
  // Bump to remount JSON editors on reset so their local raw text
  // re-initializes from the saved configuration.
  const [resetTick, setResetTick] = useState(0)
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
          <div className='max-w-3xl space-y-4 border-t pt-4'>
            <p className='text-sm font-medium'>{t('Configuration')}</p>
            <div className='grid gap-x-6 gap-y-4 sm:grid-cols-2'>
              {fields.map((field) => (
                <ConfigFieldInput
                  key={`${field.name}-${resetTick}`}
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
            </div>
            <div className='flex justify-end gap-2'>
              <Button
                variant='outline'
                size='sm'
                onClick={() => {
                  setDraft(guard.config ?? {})
                  setResetTick((tick) => tick + 1)
                }}
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
  let body: React.ReactNode
  if (guardsQuery.isLoading) {
    body = <LoadingState message={t('Loading...')} />
  } else if (guards.length === 0) {
    body = (
      <EmptyState
        icon={ShieldOff}
        title={t('No request guard plugins are installed.')}
        bordered
      />
    )
  } else {
    body = guards.map((guard) => <GuardCard key={guard.key} guard={guard} />)
  }
  return (
    <div className='space-y-3'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Request guards run in listed order before a channel is selected. A denied request never reaches an upstream and never reserves quota.'
        )}
      </p>
      {body}
    </div>
  )
}
