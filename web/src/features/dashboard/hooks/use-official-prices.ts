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
import { useMemo } from 'react'

import { getOfficialPrices } from '../api'
import {
  buildOfficialPriceTable,
  type OfficialModelPrice,
} from '../lib/official-prices'
import { requireServerSuccess } from '@/lib/server-error-message'

/**
 * 官方牌价表(内置缺省 + 管理员后端配置合并),供官方成本估算使用;
 * 接口失败或未配置时返回内置缺省表,行为与纯前端方案一致
 */
export function useOfficialPrices(): Record<string, OfficialModelPrice> {
  const query = useQuery({
    queryKey: ['official-prices'],
    queryFn: async () => requireServerSuccess(await getOfficialPrices()),
    staleTime: 5 * 60_000,
    retry: false,
  })
  return useMemo(
    () => buildOfficialPriceTable(query.data?.data),
    [query.data]
  )
}
