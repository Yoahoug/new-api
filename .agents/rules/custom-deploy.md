# custom 分支部署与本地环境指南

本仓库(`Yoahoug/new-api`,分支 `custom`)是上游 `QuantumNous/new-api` 的二改 fork。
本文档记录二改的版本号约定、本地开发环境、服务器部署流程与历史决策,
供后续会话快速了解,避免重复询问。

**最重要的一条规则:任何推送到服务器触发构建/部署的操作,必须等用户明确发出构建/部署指令才能执行(单独发或附带在其他指令里均可)。只写代码、提交、推送到 GitHub 不需要确认,但"服务器拉取 + 重建 + 切换生产容器"必须由用户指令触发。**

## 版本号约定

格式:`<上游基线版本>-custom.<二改序号>`,写在仓库根 `VERSION` 文件(构建时通过
`-X common.Version=$(cat VERSION)` 注入,状态接口 `/api/status` 返回)。

- 当前基线:上游 tag `v1.0.0-rc.40`(`custom` 分支从它分出,之后上游又有新 commit 需自行 merge)
- 当前版本:`v1.0.0-rc.40-custom.2`
- 规则:每次要部署的二改发布,序号 +1(custom.2 → custom.3);
  合并上游后,基线更新为合并时上游所在 tag,序号从 1 重新计
- 查上游基线:`git describe --tags --match "v1*" <分支基于的 commit>` 或
  `git merge-base custom upstream/main` 后找包含它的最新 tag
- 前端对 `-custom.N` 的处理:`web/src/features/system-update/releases.ts` 里
  `splitCustomVersion()` 拆出基线版本与序号;更新检查用基线与上游 release 比较

## 顶栏版本徽标与更新弹窗(二改改动)

- 顶栏不再复用上游 `SystemUpdateAction presentation='version'`(它把二改版本判为
  "未知版本",效果差),改为 `web/src/components/layout/components/custom-version-badge.tsx`:
  显示完整版本号 + 「二改版」徽标,点击弹 popover(当前版本/上游基线/二改序号)
- 系统设置→维护里的上游更新检查保留;系统更新弹窗已支持二改版本:
  显示「二改版」徽标 + 上游基线,用基线版本与上游 release 比较
- i18n 相关 key:`Custom build` / `Upstream base` / `Custom build number` /
  `Upstream base: {{version}}`(7 个 locale 都有)

## 二改功能清单(与上游的差异)

全部在 `custom` 分支,提交历史可查;核心:

- `GET /api/data/today`:按 用户×模型 聚合的"今天 0 点起"用量
  (controller/usedata.go `GetTodayUsage`、model/log.go `GetUserDayUsage`;
  cache_tokens 跨方言提取 `dayUsageCacheExpr()`,支持 SQLite/MySQL/PostgreSQL/ClickHouse)
- 数据看板"今日用量"卡:5 格(今日请求/消耗/TOKENS/缓存命中率/平均 RPM),
  底部折叠条为官方牌价估算(DeepSeek/智谱GLM,CNY,按模型);管理员显示全站,用户显示自己
- 概览页:"今日消耗"用真按天口径(修复了 `computeTimeRange(1, useStartOfDay)`
  实际返回 [昨天0点,今天23:59] 的坑);右侧面板有个人官方计费估算块
  (`official-cost-self-panel.tsx`)
- 用户列表"今日消耗"列(`web/src/features/users/hooks/use-today-quota.ts`,
  同 username 多行要累加不能覆盖)
- 官方价目表在前端 `web/src/features/dashboard/lib/official-prices.ts`
  (CNY,含 cachedInput 价),调价不用动后端
- TanStack devtools `initialIsOpen={false}`(web/src/routes/__root.tsx)

## i18n 修改注意事项(必须)

`web/src/i18n/locales/*.json` 中 key `footer.new\u0061pi.projectAttributionSuffix`
是受保护品牌标识(AGENTS.md 保护条款),JSON 里以 `footer.new\u0061pi...` 转义形式存在。
用 python `json.dumps` 重写文件会把 `\u0061` 解码成 `a`,破坏原字节。**每次改 locale 后必须还原**:

```python
enc = raw.replace(b'"footer.newapi.projectAttributionSuffix"',
                  b'"footer.new\\u0061pi.projectAttributionSuffix"')
```

## 本地开发环境

- 前端 dev server:`cd web && bun run dev`(端口 5173/3001,API 代理到本地后端)
- 本地后端:
  ```bash
  go build -o /tmp/newapi-local .
  SQL_DSN='postgresql://root:123456@localhost:15432/new-api?sslmode=disable' \
  REDIS_CONN_STRING='redis://:123456@localhost:16379' PORT=3000 \
  nohup /tmp/newapi-local --log-dir /tmp/newapi-local-logs > /tmp/newapi-local.log 2>&1 &
  ```
- 本地 Docker(Docker Desktop/OrbStack):postgres 15 宿主 15432(root/123456,卷
  newapi-local-pgdata)、redis 宿主 16379(密码 123456),容器名 newapi-local-*
- 服务器数据已 dump 到 `.local-dev/newapi-dump.dump`(已在 .gitignore)
- 测试账号(仅本地库):yoahoug / local-test-1234(管理员)、testuser / Test12345678
- 注意:`go build` 必须在仓库根目录执行;`web/dist` 为空时后端可构建但页面为占位

### 本地验证流程

1. 前端:`cd web && bun run typecheck`(tsgo -b)、`bun run lint`
   (基线 66 warnings / 182 errors,新代码不得新增)、`bunx vitest run <相关测试>`
2. 后端:`go build .`;涉及 relaykit 时 `cd relaykit && GOWORK=off go build ./...`
3. 浏览器验证:登录页协议勾选框无法用 locator 点击,需截图后坐标点击
   (勾选框约 452,467,登录按钮约 640,408);后端重启会使 session 失效,需重新登录

## 服务器部署(server-ops skill,10.66.66.66)

服务器架构(2026-09-23 起):

- 代码:`/data/appdata/new-api-custom/`(fork clone,`git fetch origin custom && git reset --hard origin/custom` 同步;GitHub 需走代理 `git -c http.proxy=http://127.0.0.1:7890`)
- 生产:`/data/appdata/new-api/`(compose 三件套:`docker-compose.yml` 上游原版 +
  `docker-compose.override.yml` 容器改名 + `docker-compose.deploy.yml` GHCR 镜像 override)
- **镜像:`ghcr.io/yoahoug/new-api-custom:local`,由 GitHub Actions 构建**
  (workflow `.github/workflows/custom-docker.yml`:push 到 custom 分支且代码变更时
  自动构建 amd64 并推 GHCR,带 GHA 缓存;workflow 文件必须同时存在于 main
  (默认分支,GitHub 只在默认分支注册 workflow)和 custom 分支)
- 实测速度:无缓存 5分51秒;吃 GHA 缓存 32秒;服务器匿名拉取 15.6 秒
  (仓库 public,包继承 public,服务器**无需 docker login**)
- 旧的本机构建配置备份在 `docker-compose.deploy.yml.bak-local-build`
  (镜像 `new-api-custom:local`,应急时改回引用即可)
- postgres/redis 容器不重建,数据卷复用
- 数据库备份:`/data/appdata/new-api/backups/`(部署前做 pg_dump 到
  `pre-custom-deploy-<时间>.sql.gz`)

### 标准部署流程(需用户明确指令后执行)

```bash
# 0. 触发构建:推 VERSION/代码到 custom 分支后,Actions 自动构建
#    (或手动:gh workflow run custom-docker.yml --repo Yoahoug/new-api --ref custom)
#    等待 run 成功(吃缓存约 1 分钟)

# 1. 服务器拉新镜像
docker pull ghcr.io/yoahoug/new-api-custom:local

# 2. 备份数据库
TS=$(date +%Y%m%d-%H%M%S)
docker exec new-api-postgres sh -c "pg_dump -U root -d new-api -Fc" \
  > /data/appdata/new-api/backups/pre-custom-deploy-$TS.sql.gz

# 3. 无缝切换(停机约 5 秒)
cd /data/appdata/new-api
docker compose -f docker-compose.yml -f docker-compose.override.yml \
  -f docker-compose.deploy.yml up -d new-api

# 4. 验证
curl -s http://127.0.0.1:3000/api/status | grep version
docker ps --filter name=new-api   # healthy
docker logs new-api --since 2m    # 无 error/panic
```

部署 override(`docker-compose.deploy.yml`)要点:`build.args` 里
`HTTP_PROXY=http://host.docker.internal:7890` 等(build 容器不走 daemon 代理),
`extra_hosts: host.docker.internal:host-gateway`。

### 服务器环境坑(已修复,勿回退)

- **UFW 默认网桥**:`ufw allow in on docker0 to any port 7890 proto tcp` 已加。
  ufw 的 `on br+` 通配不匹配 `docker0`,没有这条容器连不上宿主代理
  (症状:`[UFW BLOCK] IN=docker0 DPT=7890`)
- **mihomo 透明代理**:TUN 模式 DNAT 容器 DNS(udp 53 → 198.18.0.2,fake-ip),
  容器内 apt 用环境变量代理时域名解析为 198.18.x 是正常现象,代理本身能工作;
  apt 若走显式配置用 `-o Acquire::http::Proxy=...` 已验证可行
- docker daemon 代理(`/etc/docker/daemon.json` 未配 registry 代理)只影响
  拉镜像,不影响 build 容器内部网络

## 历史决策(勿重复讨论)

- 数据/数据库与官方版本兼容:所有 schema 变更走 GORM 迁移,三数据库兼容
- 个人数据只放概览页,数据看板管理员看全站、用户看自己
- 官方牌价估算放前端(便于合并上游、调价不重编译);后端只提供通用聚合端点
- `/api/data/token-usage` 端点已删除(被 /api/data/today 取代)
- 生产部署从官方镜像 `calciumion/new-api:latest` 切换为 fork 本地构建
