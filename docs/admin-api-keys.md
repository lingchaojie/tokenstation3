# 管理员 API 密钥

受限管理员密钥通过 `x-api-key` 请求头验证，供自动化工具操作已授权的管理资源。模型调用使用用户 API Key，管理密钥本身需要通过管理员登录会话创建和管理。授权以 HTTP 方法、路由和资源规则为准，新接口须单独登记。

## 创建与配置

管理员登录后，在「系统设置 → 安全」的管理员访问密钥区域创建密钥。权限目录和分组加载成功后才能创建。

1. 填写名称、备注、到期时间及允许的 IP/CIDR。到期时间使用本地时间，留空表示永不过期；IP 列表留空表示不限制来源 IP。
2. 添加资源规则，分别选择操作、分组、账户类型和平台。新建规则不勾选任何操作或账户类型，全站权限也全部关闭。
3. 按需独立勾选全站权限，保存后立即复制完整密钥。完整密钥仅在创建或轮换的当前响应中出现，关闭展示后不能从列表重新读取。

受限密钥的生命周期接口只接受管理员 JWT 会话。登录后发送 `Authorization: Bearer <ADMIN_JWT>`，请求中省略 `x-api-key`。创建和更新请求必须符合完整的 JSON 结构，未知字段会被拒绝。成功响应带有 `Cache-Control: no-store`。

## 受限密钥生命周期

| 方法和路径 | 结果 |
| --- | --- |
| `GET /api/v1/admin/settings/admin-api-keys` | 返回 `items` 和 `permissions`，其中 `permissions` 是当前授权目录 |
| `POST /api/v1/admin/settings/admin-api-keys` | 以当前管理员为创建者，返回 `key` 和 `item` |
| `PUT /api/v1/admin/settings/admin-api-keys/:id` | 提交完整配置和当前 `updated_at`，返回更新后的密钥记录 |
| `POST /api/v1/admin/settings/admin-api-keys/:id/rotate` | 轮换密钥，返回新 `key` 和 `item` |
| `DELETE /api/v1/admin/settings/admin-api-keys/:id` | 永久撤销密钥 |
| `DELETE /api/v1/admin/settings/admin-api-keys/:id/record` | 从管理列表移除已撤销记录 |

编辑或启停受限密钥时，将读取记录时收到的 `updated_at` 字符串原样放入 PUT 请求体，包括小数秒。创建、更新和轮换的响应均返回可用于下一次编辑的实际版本。缺失或无效版本返回 HTTP `400`。版本已过期时返回 HTTP `409` 和 `ADMIN_KEY_CONFLICT`，请重新读取记录并基于当前权限重新编辑。对支持幂等键的写操作，应在同一密钥和同一授权策略下重试。

## 旧版管理员 API Key

原有单一管理员 Key 沿用原来的鉴权和接口。管理员登录后可在「系统设置 → 安全」的独立区域查看状态、首次生成、轮换或删除旧版 Key。已有旧版 Key 的客户端继续使用 `x-api-key`。旧版接口也保留管理员 JWT 鉴权。

| 方法和路径 | 结果 |
| --- | --- |
| `GET /api/v1/admin/settings/admin-api-key` | 返回 `exists` 和 `masked_key` |
| `POST /api/v1/admin/settings/admin-api-key/regenerate` | 首次生成或轮换旧版 Key，在当前响应中返回完整的 `key` |
| `DELETE /api/v1/admin/settings/admin-api-key` | 删除旧版 Key |

## 自动化查询与操作权限

以下账户权限均放在资源规则中，按同一规则的分组、账户类型和平台校验；分组权限只按分组和平台校验。升级不会给已有密钥自动增加这些权限。

| 权限 | 用途 |
| --- | --- |
| `accounts.identity.read` | 账户详情可独立返回账号 ID、邮箱、套餐、订阅到期和创建时间。列表及模型列表需要 `accounts.read`。 |
| `accounts.usage.read` | 单个及批量账户额度查询、主动刷新，以及列表/详情中的周额度快照 |
| `accounts.stats.read` | 单个/批量今日统计与历史统计，包含请求数、Token 和费用 |
| `accounts.options.write` | 创建 OpenAI OAuth 账户时写入已列明的身份和连接选项 |
| `accounts.rate.write` | 创建/修改账户计费倍率 |
| `groups.list.read` | 所选分组的 ID、名称、平台与状态，不含费率或模型配置 |
| `groups.capacity.read` | 所选启用分组的容量；统计整个分组，不按账户类型裁剪 |
| `openai.quota.read` | OpenAI 额度、点数及重置次数查询和展示快照刷新 |
| `openai.quota.reset` | 执行 OpenAI 上游额度重置，可能消耗可用次数 |
| `openai.referrals.read` | 查询邀请资格与名额并刷新展示快照 |
| `openai.referrals.invite` | 实际发送邀请；不由邀请查询权限附带授予 |

`openai.tokens.refresh` 是独立的**全站权限**：仅刷新调用方提交的 OpenAI 令牌并返回新令牌，不读取服务器已有账户凭证。指定代理必须存在、启用且未过期；无效代理不会静默回退为直连。它与 `accounts.refresh`（刷新已保存的授权账户凭证）不同。

账户列表接受 `group` 筛选，省略或 `group=` 表示不指定分组，`-1` 表示未分组；筛选不会扩大授权范围。分页返回 `items/total/page/page_size/pages`。周额度是当前快照（`codex_usage_updated_at`、`codex_7d_used_percent`、`codex_7d_reset_at`），不是历史事件记录；缺失不伪装为零用量。

批量查询一次接受 1 至 100 个账户 ID，先验证所有账户范围，再读取统计、缓存或访问上游。混入未授权账户时整批拒绝。受限 OpenAI 额度刷新只调用额度查询接口，不发送模型探测请求。只读额度权限不授权消耗重置次数、发送邀请或恢复账户状态及调度。影子账户借用母账户凭证时，母账户也必须有对应操作权限。重置或发送邀请后的响应仍分别检查额度或邀请读取权限，身份信息另行验权；响应不会返回完整凭证、任意 `extra` 或原始上游错误。

模型和端点分布的 `actual_cost` 表示用户实扣，`account_cost` 表示供应商账号成本，`cost` 表示标准费用。按账号筛选时也使用这些口径。`GET /api/v1/admin/accounts/:id/stats` 的 `history.actual_cost` 和 `summary.total_cost` 保留原有账号成本含义，用户实扣仍分别由 `history.user_cost` 和 `summary.total_user_cost` 提供。该接口新增可选的 `start_date`、`end_date` 和 `timezone`，两个日期都是包含在内的报告日期，便于用户明细查询使用同一时间范围。

`accounts.options.write` 仅开放创建 OpenAI OAuth 账户时的 `extra.email`、`openai_oauth_responses_websockets_v2_mode`、`openai_oauth_responses_websockets_v2_enabled` 和 `openai_long_context_billing_enabled`，不能任意写入 `extra`、额度快照、影子关系或声明隐私设置成功。为兼容已有客户端，字符串 `privacy_mode` 入参会被移除，隐私状态仍由服务端原流程确认。创建倍率独立需要 `accounts.rate.write`，写入凭证及身份元数据仍遵循对应凭证权限。

## 账户规则如何匹配

对于同一项账户操作，一条规则内部必须同时满足以下条件（AND）：

- 该操作出现在 `permissions` 中。
- 账户类型出现在 `account_types` 中。空列表表示没有账户操作权限，而非所有类型。
- `platforms` 为空，或者账户平台命中其中一项。
- 已分组账户至少命中一个所选分组，或规则启用了 `all_groups`；未分组账户必须显式启用 `include_ungrouped`。

多条规则之间是 OR：任一完整规则允许该项操作即可。不能将一条规则中的账户类型、另一条规则中的分组或平台拼起来授权。不同操作会分别检查其所需权限；写入凭证、修改网络、状态、调度、模型和分组关联不是「编辑名称与备注」的附带能力。

`all_groups` 包含未来新增的分组，但不自动包含未分组账户。新建账户的每个目标分组、以及调整关联时新增或移除的目标分组，还必须通过对应操作的分组检查。

**共享账户仍是一个账户资源。** 账户关联多个分组时，命中其中一个授权分组可能允许对该账户执行已授权操作。改凭证、改状态、刷新或删除会影响所有使用该账户的分组。界面会明确提示这一点；请按共享影响授予操作。受限账户响应只返回允许读取的分组关联，不代表该账户没有其他关联。

## 全站权限和响应边界

`global_permissions` 与资源规则独立，不受所选分组或账户类型约束。例如 `users.read`、`dashboard.read`、`usage.read` 可读取全站相应数据。选择某个分组不会将这些权限限制为该分组；反过来，选择分组也不会隐式获得这些权限。

以本页的权限与路由表为准。支付配置、订单操作、注册邮箱黑名单批量停用及密钥管理均须走各自的管理入口。

受限响应按用途收窄：

- 账户读取返回基本管理信息和模型映射，不返回 OAuth token、API 密钥、任意 `extra` 或完整凭证。`accounts.credentials` 是写入权限，不提供读取凭证的能力。
- 代理候选包含 ID、名称、协议、主机、端口、状态与到期时间，不包含代理认证用户名或密码。
- 用户资料采用字段白名单；联系方式需要 `users.read`，余额字段需要 `users.balance.read`，分组配置字段需要 `groups.read`。用户响应不嵌入订阅和用量关联数据，这些能力由各自接口及权限控制。
- 订阅响应不嵌入用户、分配管理员或完整分组；仅有订阅或平台额度写权限时，成功响应只确认资源 ID，不附带独立读取权限的数据。
- 用量记录仅返回请求自身的用量、计费快照及关联 ID，不嵌入用户 API Key、账户凭证或用户财务资料。优惠码使用记录及公告阅读记录也只返回记录字段与用户 ID。
- 请求错误详情等敏感运维数据有单独权限。全站读取权限仍可能包含业务敏感数据，应只授予任务确实需要的项。

## 密钥存储、轮换与撤销

完整密钥由随机字节生成，数据库保存 SHA-256 摘要及用于展示和定位的前缀，不保存可供重显的完整密钥。客户端应把密钥放入密钥管理器或运行环境的秘密变量中。

密钥管理员可通过修改 `enabled` 停用或重新启用密钥。到期、停用或来源 IP 不符合规则时，认证会失败。来源 IP 由服务端的可信代理解析。轮换立即使旧密钥失效，并返回一次性新密钥。

创建者必须是启用中的管理员。停用或降级创建者会在同一事务中永久撤销其签发的受限密钥。以后即使恢复管理员身份，也需要签发新密钥。该规则仅适用于受限密钥。只有已撤销密钥才可以删除列表记录，历史操作审计会继续保留。

认证失败通常返回 `401`。有效密钥访问目录未登记或未授予的操作时返回 `403`。对于单个未授权账户或分组，处理器也可能返回 `404`。操作审计记录受限密钥标识，并省略请求体。

编辑时若没有修改到期时间，前端会保留服务端原始值，避免将秒数、微秒或时区信息截断。

## 代理查询与自动选择

`proxies.read` 是全站权限，与账户的 `accounts.network` 分开。前者用于获取代理候选信息，后者用于配置已授权账户的接入地址、代理与连接选项。

受限密钥更新已有账户的 `base_url` 时，新地址必须与已有显式地址同源。路径和查询参数可以修改，协议、主机和有效端口必须相同。已有账户首次添加显式地址、清除显式地址或更换来源时，请使用常规管理员入口。普通和批量更新均执行此检查。

- `GET /api/v1/admin/proxies` 使用 `page`、`page_size` 和 `search`，默认每页 50，最多 100。
- `GET /api/v1/admin/proxies/all` 为兼容客户端返回数组；显式传入分页参数时按分页查询，分页信息在 `X-Total-Count`、`X-Page`、`X-Page-Size` 和 `X-Total-Pages` 响应头中。
- `/proxies/all` 未传分页参数时，从全池重新随机抽样最多 50 个候选，不排除已被账户使用的代理；响应设置 `Cache-Control: no-store`。
- `ids` 可回显已选代理，一次最多 100 个不同 ID，按请求顺序返回实际存在的记录。

受限账户写入与 Codex 导入支持 `proxy_mode`：`direct`（直连）、`specified`（正数 `proxy_id`）、`random`（随机）、`least_used`（最少使用）。自动选择由服务端在账户写事务内从启用且未过期的代理中完成，无可用代理则失败。自动模式不能同时指定正数 `proxy_id`；旧字段 `random_proxy: true` 兼容随机模式，不能与其他模式冲突。随机候选查询与写入时自动选择是两个独立行为。

## Codex 会话导入

`POST /api/v1/admin/accounts/import/codex-session` 对受限密钥执行独立的导入验权流程。

- 必须明确提供 `group_ids`；新账户固定为 `openai` 平台、`oauth` 类型，目标分组逐一检查。
- 新建需要 `accounts.create`、`accounts.credentials` 和 `accounts.schedule`。更新匹配账户会重新检查当前资源和对应写权限；调整关联还需要 `accounts.groups`。
- `base_url`、代理、指纹或 WebSocket 连接配置需要 `accounts.network`。导入匹配已有账户时，`base_url` 与普通更新使用同一来源检查。`model_mapping` 需要 `accounts.models.write`。
- `credential_extras` 只接受独立授权的 `base_url`、`model_mapping`；`extra` 只开放已登记的 Codex 指纹及 WebSocket 选项，不支持任意字段透传或跳过混合渠道风险检查。
- 导入只按当前条目的身份查询受限候选，响应不返回候选凭证。更新已有账户时仍会在写入阶段重验身份与权限。

## API 示例

以下域名、密钥、JWT、ID 和会话内容均为占位符。通用成功响应使用系统现有响应封装。

使用管理员会话创建受限密钥。

```bash
curl 'https://example.invalid/api/v1/admin/settings/admin-api-keys' \
  -H 'Authorization: Bearer <ADMIN_JWT>' \
  -H 'Content-Type: application/json' \
  --data '{
    "name": "只读 OAuth 工具",
    "description": "读取指定分组的 OpenAI OAuth 账户",
    "enabled": true,
    "expires_at": null,
    "allowed_ips": ["192.0.2.10/32"],
    "policy": {
      "rules": [{
        "permissions": ["accounts.read"],
        "group_ids": [123],
        "all_groups": false,
        "include_ungrouped": false,
        "account_types": ["oauth"],
        "platforms": ["openai"]
      }],
      "global_permissions": []
    }
  }'
```

使用受限密钥读取账户。

```bash
curl 'https://example.invalid/api/v1/admin/accounts?page=1&page_size=20' \
  -H 'x-api-key: <SCOPED_ADMIN_KEY>'
```

使用已授权的密钥新建 Codex OAuth 账户，由服务端随机选择代理。

```bash
curl 'https://example.invalid/api/v1/admin/accounts/import/codex-session' \
  -H 'x-api-key: <SCOPED_ADMIN_KEY>' \
  -H 'Content-Type: application/json' \
  --data '{
    "content": "<CODEX_SESSION_CONTENT>",
    "name": "导入账户",
    "group_ids": [123],
    "update_existing": false,
    "proxy_mode": "random"
  }'
```

## 权限与路由

下表路径均以 `/api/v1/admin` 为前缀。权限同时校验 HTTP 方法和完整路由。多项权限可能对应同一路由，具体资源、字段和返回数据仍各自验权。分组权限放在资源规则中，全站权限放在 `global_permissions` 中。

### 账户权限

| 权限 | 方法和相对路径 |
| --- | --- |
| `accounts.identity.read` | `GET /accounts/:id` |
| `accounts.usage.read` | `GET /accounts/:id/usage`<br>`POST /accounts/usage/batch` |
| `accounts.stats.read` | `GET /accounts/:id/today-stats`<br>`POST /accounts/today-stats/batch`<br>`GET /accounts/:id/stats` |
| `openai.quota.read` | `GET /openai/accounts/:id/quota`<br>`POST /openai/accounts/:id/quota/refresh` |
| `openai.quota.reset` | `POST /openai/accounts/:id/reset-quota` |
| `openai.referrals.read` | `POST /openai/accounts/:id/referrals/refresh` |
| `openai.referrals.invite` | `POST /openai/accounts/:id/referrals/invite` |
| `accounts.options.write` | `POST /accounts` |
| `accounts.rate.write` | `POST /accounts`<br>`PUT /accounts/:id` |
| `accounts.read` | `GET /accounts`<br>`GET /accounts/:id`<br>`GET /accounts/:id/models` |
| `accounts.create` | `POST /accounts`<br>`POST /accounts/import/codex-session` |
| `accounts.update` | `PUT /accounts/:id` |
| `accounts.credentials` | `PUT /accounts/:id` |
| `accounts.models.write` | `PUT /accounts/:id`<br>`POST /accounts/bulk-update` |
| `accounts.network` | `PUT /accounts/:id` |
| `accounts.status` | `PUT /accounts/:id`<br>`POST /accounts/:id/schedulable` |
| `accounts.schedule` | `PUT /accounts/:id` |
| `accounts.refresh` | `POST /accounts/:id/refresh` |
| `accounts.recover` | `POST /accounts/:id/clear-error` |
| `accounts.delete` | `DELETE /accounts/:id` |
| `accounts.groups` | `PUT /accounts/:id` |

### 分组权限

| 权限 | 方法和相对路径 |
| --- | --- |
| `groups.list.read` | `GET /groups`<br>`GET /groups/all`<br>`GET /groups/:id` |
| `groups.capacity.read` | `GET /groups/capacity-summary` |

### 全站权限

| 权限 | 方法和相对路径 |
| --- | --- |
| `openai.tokens.refresh` | `POST /openai/refresh-token` |
| `proxies.read` | `GET /proxies`<br>`GET /proxies/all`<br>`GET /proxies/:id` |
| `models.defaults.read` | `GET /accounts/antigravity/default-model-mapping` |
| `dashboard.read` | `GET /dashboard/snapshot-v2`<br>`GET /dashboard/stats`<br>`GET /dashboard/realtime`<br>`GET /dashboard/trend`<br>`GET /dashboard/models`<br>`GET /dashboard/groups`<br>`GET /dashboard/api-keys-trend`<br>`GET /dashboard/users-trend`<br>`GET /dashboard/users-ranking`<br>`POST /dashboard/users-usage`<br>`POST /dashboard/api-keys-usage`<br>`GET /dashboard/user-breakdown` |
| `users.read` | `GET /users`<br>`GET /users/:id`<br>`GET /users/:id/rpm-status`<br>`GET /users/:id/attributes` |
| `users.balance.read` | `GET /users/:id/balance-history` |
| `users.balance.write` | `POST /users/:id/balance` |
| `users.quotas.read` | `GET /users/:id/platform-quotas` |
| `users.quotas.write` | `PUT /users/:id/platform-quotas`<br>`POST /users/:id/platform-quotas/reset` |
| `groups.read` | `GET /groups`<br>`GET /groups/all`<br>`GET /groups/:id`<br>`GET /groups/:id/rate-multipliers` |
| `usage.read` | `GET /usage`<br>`GET /usage/stats`<br>`GET /users/:id/usage` |
| `subscriptions.read` | `GET /subscriptions`<br>`GET /subscriptions/:id`<br>`GET /subscriptions/:id/progress`<br>`GET /groups/:id/subscriptions`<br>`GET /users/:id/subscriptions` |
| `subscriptions.assign` | `POST /subscriptions/assign`<br>`POST /subscriptions/bulk-assign` |
| `subscriptions.extend` | `POST /subscriptions/:id/extend` |
| `subscriptions.revoke` | `POST /subscriptions/:id/revoke`<br>`DELETE /subscriptions/:id` |
| `subscriptions.restore` | `POST /subscriptions/:id/restore` |
| `subscriptions.reset` | `POST /subscriptions/:id/reset-quota` |
| `announcements.read` | `GET /announcements`<br>`GET /announcements/:id`<br>`GET /announcements/:id/read-status` |
| `announcements.write` | `POST /announcements`<br>`PUT /announcements/:id` |
| `announcements.delete` | `DELETE /announcements/:id` |
| `promo.read` | `GET /promo-codes`<br>`GET /promo-codes/:id`<br>`GET /promo-codes/:id/usages` |
| `promo.write` | `POST /promo-codes`<br>`PUT /promo-codes/:id` |
| `promo.delete` | `DELETE /promo-codes/:id` |
| `ops.read` | `GET /ops/concurrency`<br>`GET /ops/user-concurrency`<br>`GET /ops/account-availability`<br>`GET /ops/realtime-traffic`<br>`GET /ops/ingress-rejections`<br>`GET /ops/ingress-rejections/health`<br>`GET /ops/auth-cache-invalidation/health`<br>`GET /ops/dashboard/snapshot-v2`<br>`GET /ops/dashboard/overview`<br>`GET /ops/dashboard/throughput-trend`<br>`GET /ops/dashboard/latency-histogram`<br>`GET /ops/dashboard/error-trend`<br>`GET /ops/dashboard/error-distribution`<br>`GET /ops/dashboard/openai-token-stats` |
| `ops.errors.read` | `GET /ops/errors`<br>`GET /ops/errors/:id`<br>`GET /ops/request-errors`<br>`GET /ops/request-errors/:id`<br>`GET /ops/request-errors/:id/upstream-errors`<br>`GET /ops/upstream-errors`<br>`GET /ops/upstream-errors/:id`<br>`GET /ops/requests` |
| `ops.errors.resolve` | `PUT /ops/errors/:id/resolve`<br>`PUT /ops/request-errors/:id/resolve`<br>`PUT /ops/upstream-errors/:id/resolve` |
| `ops.alerts.read` | `GET /ops/alert-rules`<br>`GET /ops/alert-events`<br>`GET /ops/alert-events/:id` |
| `ops.alerts.write` | `POST /ops/alert-rules`<br>`PUT /ops/alert-rules/:id`<br>`DELETE /ops/alert-rules/:id`<br>`PUT /ops/alert-events/:id/status`<br>`POST /ops/alert-silences` |
| `system.read` | `GET /system/version`<br>`GET /system/check-updates`<br>`GET /system/rollback-versions` |
