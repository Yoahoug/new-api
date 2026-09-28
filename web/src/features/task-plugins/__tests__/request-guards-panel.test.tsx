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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { updateRequestGuardConfig } from '../api'
import { RequestGuardsPanel } from '../components/request-guards-panel'
import type { RequestGuardItem } from '../types'

vi.mock('../api', () => ({
  listRequestGuards: vi.fn(async () => []),
  updateRequestGuardConfig: vi.fn(),
}))

const mockedGuard: RequestGuardItem = {
  key: 'model-policy',
  name: 'Model Policy',
  version: '1.0.0',
  hasIcon: false,
  description: { en: 'Limit which models each group may call' },
  guard: {
    priority: 100,
    authorize: 'authorize',
    configFields: [
      {
        name: 'groupModelRules',
        type: 'array',
        description: { en: 'Allowed models per group' },
      },
      {
        name: 'denyMessage',
        type: 'string',
        description: { en: 'Deny message' },
      },
      {
        name: 'strict',
        type: 'boolean',
        description: { en: 'Strict mode' },
      },
    ],
  },
  config: { denyMessage: 'not allowed' },
}

const queryClients: QueryClient[] = []

function renderPanel(guards: RequestGuardItem[]): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Number.POSITIVE_INFINITY },
    },
  })
  queryClient.setQueryData(['request-guards'], guards)
  queryClients.push(queryClient)
  render(
    <QueryClientProvider client={queryClient}>
      <RequestGuardsPanel />
    </QueryClientProvider>
  )
  return queryClient
}

afterEach(() => {
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
  vi.clearAllMocks()
})

describe('RequestGuardsPanel', () => {
  test('shows the empty state when no guard is installed', () => {
    renderPanel([])
    expect(
      screen.getByText('No request guard plugins are installed.')
    ).toBeVisible()
  })

  test('renders typed config widgets inside the bounded two-column form', () => {
    renderPanel([mockedGuard])

    expect(screen.getByText('Model Policy')).toBeVisible()
    const rulesArea = screen.getByLabelText('Allowed models per group')
    expect(rulesArea.tagName).toBe('TEXTAREA')
    expect(rulesArea).toHaveClass('font-mono')
    expect(rulesArea.closest('[data-slot="field"]')).toHaveClass(
      'sm:col-span-2'
    )
    expect(rulesArea.closest('.max-w-3xl')).not.toBeNull()

    const messageInput = screen.getByLabelText('Deny message')
    expect(messageInput.tagName).toBe('INPUT')
    expect(messageInput).toHaveValue('not allowed')
    expect(
      screen.getByRole('switch', { name: 'Strict mode' })
    ).toBeVisible()
  })

  test('saves the edited draft with parsed JSON and toggled boolean', async () => {
    const user = userEvent.setup()
    vi.mocked(updateRequestGuardConfig).mockResolvedValue({})
    renderPanel([mockedGuard])

    await user.type(
      screen.getByLabelText('Allowed models per group'),
      '{[}"a"{]}'
    )
    await user.click(screen.getByRole('switch', { name: 'Strict mode' }))
    await user.click(screen.getByRole('button', { name: 'Save configuration' }))

    await waitFor(() => {
      expect(updateRequestGuardConfig).toHaveBeenCalledWith('model-policy', {
        denyMessage: 'not allowed',
        groupModelRules: ['a'],
        strict: true,
      })
    })
  })

  test('reset restores the saved server configuration', async () => {
    const user = userEvent.setup()
    renderPanel([mockedGuard])

    await user.type(
      screen.getByLabelText('Allowed models per group'),
      '{[}"a"{]}'
    )
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(screen.getByLabelText('Allowed models per group')).toHaveValue('')
    expect(screen.getByLabelText('Deny message')).toHaveValue('not allowed')
    expect(
      screen.getByRole('switch', { name: 'Strict mode' })
    ).not.toBeChecked()
  })
})
