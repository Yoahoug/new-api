# 插件开发方案（任务插件 + 请求守卫 + 请求拦截器）

面向在本仓库开发插件的 agent 与维护者。运行时契约的权威文档是
`docs/plugin-api/v1.md`、`docs/plugin-api/v1.d.ts`、`docs/plugin-api/v1.schema.json`；
本文件描述**工程方法**：如何写、验证、发布一个插件，以及权限模块（请求守卫）
的设计边界。设计对标 router-for-me/CLIProxyAPI 的插件宿主（能力声明 + 请求
拦截链 + 管理面），落地为 new-api 的三种插件类型。

## 三种插件类型

同一个 Sobek 沙箱、同一套 registry/generation/上传/同步设施，三种互斥的 `meta`：

| | 任务插件（task plugin） | 请求守卫（request guard） | 请求拦截器（request interceptor） |
|---|---|---|---|
| 回答的问题 | 上游任务怎么驱动 | 这个请求能不能跑 | 放行的请求长什么样 |
| 关键声明 | `models`、`fetchMode`、`routes`/`protocols` | `guard` | `interceptor` |
| 必需导出 | `buildSubmitRequest`、`parseSubmitResponse`、`parseTaskResult`、`buildQueryRequest`（或 batch 对） | `authorize`（`meta.guard.authorize` 指定名字） | `intercept`（`meta.interceptor.intercept`） |
| 可选导出 | `listArtifacts`+`buildContentRequest`、`parseSubmitEvent*` | `complete`（`meta.guard.complete`） | `complete`（`meta.interceptor.complete`） |
| 绑定渠道 | 是（type-61 / type-60 扩展） | 否（`TaskPluginBindableError` 拒绝） | 否 |
| 计费 | 上报 usage facts，host 定价结算 | 不涉及计费 | 不涉及计费 |
| 生效路径 | channel 选择之后执行 driver | relay 链 `TokenAuth → PluginRequestGuard → ModelRequestRateLimit → Distribute` | `DoApiRequest`/`DoFormRequest` 内、header override 之后、`doRequest` 之前 |

约束是硬性的：`rejectTaskPluginSurface` 拒绝守卫/拦截器声明任务插件面
（models/routes/protocols/usage/channelTypes/baseUrl/allowedHosts/upstreams），
守卫与拦截器互斥（`cannot declare both guard and interceptor`）。守卫必须
`requiredCapabilities: ["request-guard@1"]`，拦截器必须
`["request-interceptor@1"]`。

## 请求拦截器（对齐 CPA request.intercept）

CPA 的 RequestInterceptor / codex-service-tier / tool-image-relay 一类"请求
整形"插件的对应物。**每次尝试**（含重试）在渠道选定、模型映射、协议转换、
param override、header override 全部完成后执行：

- 请求视图 `InterceptRequest`：upstreamBaseUrl、**upstreamModel（映射后的
  上游模型名**，拦截器跟随渠道 model_mapping）、retryIndex、channelId/Type、
  headers（host 已组装的出站头，小写）、body（最终出站 JSON，≤4 MiB，否则
  null）、config。
- 返回 `{body?, headers?, clearHeaders?, metadata?}`：body 必须是 JSON object，
  整体替换出站载荷（host 重编码、更新 Content-Length、保持 GetBody 可重放）；
  headers 合并覆盖；`clearHeaders` 先删后加。
- **host 专有头不可改**：authorization/proxy-authorization/cookie/
  content-length/transfer-encoding/content-encoding/upgrade/connection/host/
  `*-secret`/`*-password` 在 decode 时直接拒绝——可以补厂商头，不能改鉴权。
- 失败语义：hook 抛错/坏返回/编码失败 → **该次尝试失败**
  （`do_request_failed`，skip retry），保证"未经修复的请求不会到达它修不了的
  上游"；`failOpen: true` 则跳过该拦截器继续发送。
- 链序：priority 降序、key 升序，generation 内一次排序；每个 hook 看到前一个
  hook 的改写结果（改写可组合）。
- 渠道测试（`IsChannelTest`）不跑拦截器；body 非 JSON 或超限时拦截器看不到
  body 也不阻塞请求（skip 并记 `unreadable_body`）。
- 实现接缝：`relay/channel/api_request.go` 的 `applyPluginRequestInterceptors`，
  `DoApiRequest` 与 `DoFormRequest` 都调用；`DoWssRequest`（realtime）不接。

## 守卫执行语义（实现见 `pkg/jsplugin/guard.go`）

- 链序：`priority` 降序、key 升序，**在 generation 构建时排序一次**，发布后不变。
- 请求视图 `GuardRequest`：请求 id、method、path、clientIp、userId/tokenId/
  tokenName、userGroup/usingGroup（token 覆盖优先，即计费分组）、model（解析失败
  为空）、stream、headers（小写、≤64 条、**剔除凭据头**：authorization/cookie/
  `*-key`/`*-token`/`*-secret`/`*-password`/`*-signature` 及固定清单）、body
  （JSON ≤256 KiB，否则 null）、config（管理员配置，未配置为 null）。
- 判定 `{allow, status?, code?, message?, headers?, metadata?}`：
  - `status` 缺省 403，仅接受 400–599；
  - `code` 缺省 `permission_denied`；`message` 缺省含守卫名；
  - `headers` 禁止 framing/协商/路由头（content-type、content-length、
    transfer-encoding、content-encoding、connection、upgrade、location）；
  - 返回形状错误按 hook error 处理。
- 失败语义：hook 抛错/超时/坏返回 → **fail closed**（403
  `permission_guard_error`），除非 `failOpen: true`。
- 超时：`timeoutMs` 缺省 2000，钳位 100–5000（守卫在所有请求的热路径上，短于
  任务插件的 5s 默认）。
- `exclusive: true`：该守卫 allow 即终止链条（它是这些请求的唯一裁决者）。
- `complete` hook：每个请求恰好一次终态事件
  `{requestId, outcome: denied|succeeded|failed|canceled, statusCode, guardKey,
  startedAt, completedAt}`，异步投递、context 与客户端断开（客户端断连也通知）。
  守卫用它释放并发额度；不要在 complete 里做重活。
- 中间件接入点：`middleware/plugin_guard.go`，挂在 `/v1`（含 realtime WS 组）与
  `/v1beta`，位置在 TokenAuth 之后、限流与 Distribute 之前——拒绝发生在选渠道
  与预扣额度之前，成本为零。
- 配置存储：options 表键 `TaskPluginGuardConfigs`（`{pluginKey: config}` JSON），
  经 `SetGuardConfigsOption` 原子替换快照；管理 API 按声明的 `configFields`
  校验（类型 + enum 成员 + 未声明字段拒绝）后经 `model.UpdateOption` 落库，
  多节点靠既有 options 同步收敛。
- 热加载：守卫与任务插件同走 registry/generation。上传（`POST/PUT
  /api/plugin/task`）与 30 秒 DB sync 循环都会 `ReplaceOverrides` 发布新
  generation，请求 pin 住旧 generation 直到结束——**无需重启**。开关
  `TaskPluginEnabled` 总闸同样作用于守卫（`prepareGeneration` 清空两层的路径）。

## 管理面

- `GET /api/plugin/task/guards`：已装守卫按链序列出（meta + config）。
- `PUT /api/plugin/task/guards/:key/config`：更新一个守卫的配置。
- `GET /api/plugin/task/interceptors` / `PUT /api/plugin/task/interceptors/:key/config`：拦截器的对应管理面（配置存 options 键 `TaskPluginInterceptorConfigs`）。
- `GET /api/plugin/task` 列表对守卫打 `guard` 标记；渠道绑定选项
  （`GetTaskPluginOptions`）排除守卫与拦截器。
- 前端：任务插件页 "Request guards" Tab（`request-guards-panel.tsx`），按
  configFields 渲染表单（scalar 直填，array/object 走 JSON）；拦截器管理面复用
  同一表单组件的思路，接口在 `api.ts` 的 `listRequestInterceptors` /
  `updateRequestInterceptorConfig`。

## 开发一个插件的流程（agent 友好）

1. **读契约**：`docs/plugin-api/v1.md`（守卫与拦截器章节在文末）、`v1.d.ts`。JS 语法
   约束：单文件 ESM、同步、禁止 import/async/await（`forbiddenSyntax` 会拒）。
2. **写 plugin.js**：`export const meta = {...}` + 导出 hook。参考实现：
   - 守卫：`plugins/guards/model-policy/plugin.js`（分组模型白名单 + 用户黑名单，
     内置安装）；
   - 拦截器：`examples/interceptors/opencode-session-header/plugin.js`（注入会话
     请求头）、`examples/interceptors/tool-image-relay/plugin.js`（迁移 tool 消息
     图片块，修复上游 400）——**未内置安装**，需要时按第 5 步上传；
   - 任务插件：`plugins/tasks/sora/plugin.js` 等 10 个。
3. **本地校验**：
   ```bash
   go run . plugin lint plugins/guards/<key>/plugin.js
   go run . plugin test plugins/guards/<key>/plugin.js --fixture fixture.json
   ```
   fixture 格式 `{unixNow, cases:[{name, hook, member?, args, expected?, expectedError?}]}`。
4. **单测**：守卫行为测试集中在 `pkg/jsplugin/guard_test.go`（链序/互斥/fail
   closed/作用域/热加载）与 `middleware/plugin_guard_test.go`（HTTP 层拒绝、凭据
   头剔除、body 可重读）。新语法/校验分支优先扩展现有文件，不另开新文件。
5. **上传生效**：root 页面上传或 `POST /api/plugin/task`；编译失败保留 incumbent，
   路由冲突按 conflict-tolerant 准入拒绝并留错误详情（`GET
   /api/plugin/task/runtime/status`）。
6. **配置**：守卫在 "Request guards" Tab 配置；未配置（config=null）的守卫**必须
   allow**——内置与第三方守卫都要满足"装上不改行为"。

## 内置插件注册

`plugins/embed.go` 以 `//go:embed` 注册两类内置插件目录：`tasks/`（任务插件）、
`guards/`（请求守卫），统一走 `registerEmbeddedKind`。请求拦截器**没有内置
插件**（机制在 `pkg/jsplugin/interceptor.go`，参考实现放在仓库根
`examples/interceptors/`，不上传就不生效）。注册失败 panic 拒绝启动，所以新
内置插件必须先过 lint。`plugins/builtin_plugins_test.go` 断言 tasks 目录键
集合；guards 目录暂无此类清单断言，新增内置插件不需要改测试，但**删除**内置
守卫时留意
`middleware/plugin_guard_test.go::TestInstalledRequestGuardAllowsEverythingUntilConfigured`
依赖 model-policy 存在。

## 设计边界（改写已做，其余谨慎）

- 请求**改写**已由拦截器实现（第三种插件类型），但守卫仍然不能改写：守卫拒绝
  发生在任何副作用之前的不变量不变；需要改写就写 interceptor，需要准入裁决就
  写 guard。
- 响应/SSE chunk 拦截器（CPA ResponseInterceptor / StreamChunkInterceptor）
  尚未实现，如需引入应作为第四种能力面单独设计，复用本文件的
  generation/链序/配置设施。
- 拦截器看不到凭据头之外的限制：它能**读**出站 Authorization（对诊断必要），
  但不能**改**它；headers 快照上限 64 条。
- 无每插件独立限流；插件自身过载靠 `timeoutMs` + fail closed/fail open 兜底。
