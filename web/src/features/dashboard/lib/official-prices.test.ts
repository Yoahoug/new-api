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
import { describe, expect, it } from 'vitest'

import {
  buildOfficialPriceTable,
  calculateOfficialCost,
  OFFICIAL_MODEL_PRICES,
} from './official-prices'

describe('buildOfficialPriceTable', () => {
  it('returns built-in defaults when nothing is configured', () => {
    expect(buildOfficialPriceTable(null)).toEqual(OFFICIAL_MODEL_PRICES)
    expect(buildOfficialPriceTable([])).toEqual(OFFICIAL_MODEL_PRICES)
    expect(buildOfficialPriceTable({} as never)).toEqual(
      OFFICIAL_MODEL_PRICES
    )
  })

  it('overrides built-in entries and adds new vendors by model name', () => {
    const table = buildOfficialPriceTable([
      {
        model: 'GLM-5.3',
        vendor: 'zhipu',
        vendor_label: 'Zhipu GLM',
        input: 9,
        output: 30,
      },
      { model: 'kimi-k2', vendor: 'moonshot', input: 4, output: 16 },
    ])
    expect(table['glm-5.3']).toMatchObject({
      vendor: 'zhipu',
      vendorLabel: 'Zhipu GLM',
      input: 9,
      output: 30,
    })
    expect(table['kimi-k2']).toEqual({
      vendor: 'moonshot',
      vendorLabel: undefined,
      input: 4,
      cachedInput: undefined,
      output: 16,
    })
    // 未覆盖的内置条目保留
    expect(table['deepseek-v4.1-flash']).toEqual(
      OFFICIAL_MODEL_PRICES['deepseek-v4.1-flash']
    )
  })

  it('skips entries without a model name', () => {
    const table = buildOfficialPriceTable([
      { vendor: 'v', input: 1, output: 1 },
    ] as never)
    expect(table).toEqual(OFFICIAL_MODEL_PRICES)
  })
})

describe('calculateOfficialCost with injected table', () => {
  it('prices models from the injected table and reports vendor labels', () => {
    const table = buildOfficialPriceTable([
      { model: 'kimi-k2', vendor: 'moonshot', vendor_label: 'Moonshot', input: 4, output: 16 },
    ])
    const result = calculateOfficialCost(
      [
        {
          model_name: 'kimi-k2',
          prompt_tokens: 1_000_000,
          completion_tokens: 500_000,
          cache_tokens: 0,
        },
      ],
      table
    )
    expect(result.totalCNY).toBe(4 + 8)
    expect(result.vendors).toHaveLength(1)
    expect(result.vendors[0].label).toBe('Moonshot')
    expect(result.unmatchedModels).toEqual([])
  })

  it('falls back to the built-in vendor map, then the raw vendor id', () => {
    const result = calculateOfficialCost(
      [
        {
          model_name: 'kimi-k3',
          prompt_tokens: 1_000_000,
          completion_tokens: 0,
          cache_tokens: 0,
        },
      ],
      buildOfficialPriceTable([
        // moonshot 已在内置映射中,无 label 时回退到内置 'Kimi'
        { model: 'kimi-k3', vendor: 'moonshot', input: 20, output: 100 },
      ])
    )
    expect(result.vendors[0].label).toBe('Kimi')

    const fallback = calculateOfficialCost(
      [
        {
          model_name: 'some-model',
          prompt_tokens: 1_000_000,
          completion_tokens: 0,
          cache_tokens: 0,
        },
      ],
      buildOfficialPriceTable([
        { model: 'some-model', vendor: 'unknown-vendor', input: 4, output: 16 },
      ])
    )
    expect(fallback.vendors[0].label).toBe('unknown-vendor')
  })
})
