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
// 官方 API 牌价估算用价目表(元/百万 token,人民币国内牌价)。
// 仅为个人成本参考,不代表实际扣费;价格来源为各官方定价页,手动维护。
export type OfficialVendor = 'deepseek' | 'zhipu'

export interface OfficialModelPrice {
  /** 所属厂商,用于分组计价 */
  vendor: OfficialVendor
  /** 输入单价(缓存未命中),CNY / 百万 token */
  input: number
  /** 输入单价(缓存命中),CNY / 百万 token;缺省按 input 8 折 */
  cachedInput?: number
  /** 输出单价,CNY / 百万 token */
  output: number
}

// DeepSeek: https://api-docs.deepseek.com/zh-cn/quick_start/pricing (高峰时段价;
// 空闲时段为高峰半价,这里按保守的高峰价估算)
// deepseek-v4-flash 与 deepseek-flash 同价(V4.1-Flash 承接旧 V4-Flash 请求;
// V4-Pro 自 2026-09-14 起也路由到 V4.1-Flash 并按 Flash 价计费)
export const OFFICIAL_MODEL_PRICES: Record<string, OfficialModelPrice> = {
  'deepseek-v4.1-flash': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  'deepseek-v4-flash': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  'deepseek-flash': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  'deepseek-v4-pro': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  // 智谱 GLM: https://docs.bigmodel.cn/cn/guide/start/pricing
  'glm-5.3': { vendor: 'zhipu', input: 8, cachedInput: 2, output: 28 },
  'glm-5.3-flash': { vendor: 'zhipu', input: 0.8, cachedInput: 0.23, output: 2.8 },
  'glm-5.2': { vendor: 'zhipu', input: 8, cachedInput: 2, output: 28 },
}

export const VENDOR_LABEL_KEYS: Record<OfficialVendor, string> = {
  deepseek: 'DeepSeek',
  zhipu: 'Zhipu GLM',
}

export function getOfficialModelPrice(
  modelName: string
): OfficialModelPrice | null {
  return OFFICIAL_MODEL_PRICES[modelName.toLowerCase()] ?? null
}

export interface VendorOfficialCost {
  vendor: OfficialVendor
  /** 该厂商的官方牌价估算,人民币元 */
  totalCNY: number
  /** 该厂商下已匹配的模型名 */
  models: string[]
}

export interface OfficialCostResult {
  /** 各厂商估算,只含有用量的厂商 */
  vendors: VendorOfficialCost[]
  /** 官方牌价估算合计,人民币元 */
  totalCNY: number
  /** 未匹配到价目表的模型名(不计入估算) */
  unmatchedModels: string[]
}

export function calculateOfficialCost(
  items: Array<{
    model_name: string
    prompt_tokens: number
    completion_tokens: number
    cache_tokens: number
  }>
): OfficialCostResult {
  const vendorCosts = new Map<OfficialVendor, VendorOfficialCost>()
  const unmatchedModels: string[] = []
  let totalCNY = 0

  for (const item of items) {
    const promptTokens = Number(item.prompt_tokens) || 0
    const completionTokens = Number(item.completion_tokens) || 0
    if (promptTokens + completionTokens <= 0) continue

    const price = getOfficialModelPrice(item.model_name)
    if (!price) {
      if (!unmatchedModels.includes(item.model_name)) {
        unmatchedModels.push(item.model_name)
      }
      continue
    }

    let entry = vendorCosts.get(price.vendor)
    if (!entry) {
      entry = { vendor: price.vendor, totalCNY: 0, models: [] }
      vendorCosts.set(price.vendor, entry)
    }
    if (!entry.models.includes(item.model_name)) {
      entry.models.push(item.model_name)
    }

    // 缓存命中部分按命中价,其余输入按未命中价
    const cacheTokens = Math.min(Number(item.cache_tokens) || 0, promptTokens)
    const cachedCNY = (cacheTokens / 1e6) * (price.cachedInput ?? price.input)
    const uncachedCNY = ((promptTokens - cacheTokens) / 1e6) * price.input
    const outputCNY = (completionTokens / 1e6) * price.output
    entry.totalCNY += cachedCNY + uncachedCNY + outputCNY
    totalCNY += cachedCNY + uncachedCNY + outputCNY
  }

  return {
    vendors: [...vendorCosts.values()],
    totalCNY,
    unmatchedModels,
  }
}

/** 固定人民币符号显示估算金额(不走系统货币设置,牌价本身即人民币) */
export function formatOfficialCNY(amountCNY: number): string {
  const abs = Math.abs(amountCNY)
  const digits = abs >= 1 ? 2 : 4
  return `¥${amountCNY.toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: digits,
  })}`
}
