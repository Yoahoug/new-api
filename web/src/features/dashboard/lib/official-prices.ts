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
// 仅为个人成本参考,不代表实际扣费;价格来源为各官方定价页。
// 牌价可由管理员通过 /api/official_prices 在后端维护
// (setting/official_price_setting),未配置或条目缺失时回退到内置表。
export type OfficialVendor = string

export interface OfficialModelPrice {
  /** 所属厂商,用于分组计价 */
  vendor: OfficialVendor
  /** 厂商显示名,缺省回退到 VENDOR_LABEL_KEYS */
  vendorLabel?: string
  /** 输入单价(缓存未命中),CNY / 百万 token */
  input: number
  /** 输入单价(缓存命中),CNY / 百万 token;缺省按 input 计 */
  cachedInput?: number
  /** 输出单价,CNY / 百万 token */
  output: number
}

// DeepSeek: https://api-docs.deepseek.com/zh-cn/quick_start/pricing (高峰时段价;
// 空闲时段为高峰半价,这里按保守的高峰价估算)
// deepseek-v4-flash 与 deepseek-flash 同价(V4.1-Flash 承接旧 V4-Flash 请求;
// V4-Pro 自 2026-09-14 起也路由到 V4.1-Flash 并按 Flash 价计费)
// 智谱 GLM: https://docs.bigmodel.cn/cn/guide/start/pricing (GLM-5.3-Flash 标准价,
// 限时五折 0.4/0.115/1.4 不取)
// Kimi: https://platform.kimi.com (缓存写入价未纳入估算)
// 小米 MiMo: https://mimo.mi.com/docs/zh-CN/price/pay-as-you-go (2026-09-21 核对)
// OpenAI GPT-6 Luna: $0.10/$0.50 每 1M,按 7.2 汇率折算
export const OFFICIAL_MODEL_PRICES: Record<string, OfficialModelPrice> = {
  'deepseek-v4.1-flash': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  'deepseek-v4-flash': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  'deepseek-flash': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  'deepseek-v4-pro': { vendor: 'deepseek', input: 2, cachedInput: 0.04, output: 8 },
  'glm-5.3': { vendor: 'zhipu', input: 8, cachedInput: 2, output: 28 },
  'glm-5.3-flash': { vendor: 'zhipu', input: 0.8, cachedInput: 0.23, output: 2.8 },
  'glm-5.2': { vendor: 'zhipu', input: 8, cachedInput: 2, output: 28 },
  'kimi-k3': { vendor: 'moonshot', input: 20, cachedInput: 2, output: 100 },
  'mimo-v2.6-pro': { vendor: 'xiaomi', input: 3, cachedInput: 0.025, output: 6 },
  'mimo-v2.6-flash': { vendor: 'xiaomi', input: 1, cachedInput: 0.02, output: 2 },
  'gpt-6-luna': { vendor: 'openai', input: 0.72, output: 3.6 },
}

export const VENDOR_LABEL_KEYS: Record<string, string> = {
  deepseek: 'DeepSeek',
  zhipu: 'Zhipu GLM',
  moonshot: 'Kimi',
  xiaomi: 'Xiaomi MiMo',
  openai: 'OpenAI',
}

/** /api/official_prices 返回的牌价配置项(管理员在后端维护) */
export interface OfficialPriceConfigItem {
  model: string
  vendor: string
  vendor_label?: string
  input: number
  cached_input?: number
  output: number
}

/**
 * 合并内置缺省表与服务器配置:服务器条目按模型名(忽略大小写)覆盖或新增,
 * 未配置的模型继续使用内置价,便于逐个补充模型
 */
export function buildOfficialPriceTable(
  items?: OfficialPriceConfigItem[] | null
): Record<string, OfficialModelPrice> {
  const table: Record<string, OfficialModelPrice> = {
    ...OFFICIAL_MODEL_PRICES,
  }
  if (!Array.isArray(items)) {
    return table
  }
  for (const item of items) {
    if (!item?.model) continue
    table[item.model.toLowerCase()] = {
      vendor: item.vendor,
      vendorLabel: item.vendor_label || undefined,
      input: Number(item.input) || 0,
      cachedInput:
        item.cached_input === undefined || item.cached_input === null
          ? undefined
          : Number(item.cached_input),
      output: Number(item.output) || 0,
    }
  }
  return table
}

export function getOfficialModelPrice(
  modelName: string,
  prices: Record<string, OfficialModelPrice> = OFFICIAL_MODEL_PRICES
): OfficialModelPrice | null {
  return prices[modelName.toLowerCase()] ?? null
}

export interface VendorOfficialCost {
  vendor: OfficialVendor
  /** 厂商显示名(配置 label → 内置映射 → 厂商标识) */
  label: string
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
  }>,
  prices: Record<string, OfficialModelPrice> = OFFICIAL_MODEL_PRICES
): OfficialCostResult {
  const vendorCosts = new Map<OfficialVendor, VendorOfficialCost>()
  const unmatchedModels: string[] = []
  let totalCNY = 0

  for (const item of items) {
    const promptTokens = Number(item.prompt_tokens) || 0
    const completionTokens = Number(item.completion_tokens) || 0
    if (promptTokens + completionTokens <= 0) continue

    const price = getOfficialModelPrice(item.model_name, prices)
    if (!price) {
      if (!unmatchedModels.includes(item.model_name)) {
        unmatchedModels.push(item.model_name)
      }
      continue
    }

    let entry = vendorCosts.get(price.vendor)
    if (!entry) {
      entry = {
        vendor: price.vendor,
        label:
          price.vendorLabel ??
          VENDOR_LABEL_KEYS[price.vendor] ??
          price.vendor,
        totalCNY: 0,
        models: [],
      }
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
