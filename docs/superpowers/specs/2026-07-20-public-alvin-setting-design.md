# 公开 Alvin 布尔配置接口设计

- 日期：2026-07-20
- 状态：已批准，待实施
- 范围：新增一个免登录的只读接口，从现有 `settings` 表读取固定布尔配置 `alvin`

## 背景与目标

另一个项目需要从本服务读取一个可由数据库控制的布尔值。该值与现有业务无关，变量名固定为
`alvin`，默认值为 `true`。调用方不登录，也不携带 JWT 或 API Key。

目标接口为：

```http
GET /api/v1/settings/alvin
```

成功响应沿用项目统一 envelope：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "alvin": true
  }
}
```

## 已确认的需求

- 接口公开开放，不要求登录或 API Key。
- 路径固定为 `GET /api/v1/settings/alvin`。
- 响应使用项目统一的 `code`、`message`、`data` envelope。
- 配置键固定为 `alvin`，不接受客户端传入任意 setting key。
- 默认值为 `true`。
- 管理员不需要新的前端页面；后续直接修改数据库中的记录。
- 每次请求读取数据库，不增加进程内缓存；数据库修改在下一次请求生效。

## 架构与组件

实现复用现有 Go + Gin + ent 的 setting 链路，不新增表或 schema migration。

### 设置键与默认值

在 `backend/internal/service/domain_constants.go` 增加：

```go
SettingKeyAlvin = "alvin"
```

`settings.value` 继续使用字符串存储，规范值为 `"true"` 或 `"false"`。

`SettingService.InitializeDefaultSettings` 需要同时覆盖两类部署：

新数据库和已有数据库统一走 `SetIfAbsent`：使用数据库原子的
`INSERT ... ON CONFLICT (key) DO NOTHING` 插入 `alvin=true`。`alvin` 不进入现有批量 upsert
默认值 map，避免部分初始化数据库中的已有值被覆盖；并发启动或并发写入时也不会覆盖已有记录。

默认值补齐必须是幂等操作。写入发生真实数据库错误时，初始化返回错误，保持当前 setting
初始化链路的失败语义。

### Service 读取方法

`SettingService` 新增一个职责单一的方法，例如：

```go
GetAlvin(ctx context.Context) (bool, error)
```

该方法通过 `SettingRepository.GetValue` 只读取 `SettingKeyAlvin`，不调用完整的公开设置构建逻辑，
也不把 `alvin` 加入 `/api/v1/settings/public`。

解析规则固定如下：

- 去掉首尾空格后，大小写不敏感的 `true` 返回 `true`。
- 去掉首尾空格后，大小写不敏感的 `false` 返回 `false`。
- 记录不存在、空字符串或其他非法值返回默认值 `true`。
- 非 `ErrSettingNotFound` 的 repository 错误向上返回，不用默认值掩盖数据库故障。

GET 读取不写数据库。若记录在服务启动后被删除，接口回退 `true`，直到下次启动重新补齐记录或管理员
手动插入。

### Handler 与路由

在公开 `SettingHandler` 增加 `GetAlvin` handler，并返回一个带 JSON tag 的明确响应类型：

```go
type AlvinSettingResponse struct {
    Alvin bool `json:"alvin"`
}
```

handler 调用 `response.Success` 生成统一 envelope。读取错误交给 `response.ErrorFrom`，因此真实数据库错误
返回统一 HTTP 500 响应。成功响应设置 `Cache-Control: no-store`，避免客户端或中间代理缓存后延迟观察
数据库修改。

在 `backend/internal/server/routes/auth.go` 已有的公开 `/settings` 路由组中注册：

```go
settings.GET("/alvin", h.Setting.GetAlvin)
```

该路由与 `/settings/public` 同级，注册在 JWT 认证路由组之外。它仍经过项目的通用日志、CORS、恢复、
安全响应头和 Server-Timing 中间件。

## 数据流

1. 外部项目请求 `GET /api/v1/settings/alvin`。
2. Gin 路由直接进入公开 `SettingHandler.GetAlvin`，不执行认证中间件。
3. handler 调用 `SettingService.GetAlvin`。
4. service 用唯一索引键 `alvin` 查询 `settings` 表并执行严格的布尔解析与默认回退。
5. handler 返回统一 JSON envelope。

没有通用的按 key 查询入口，因此其他 setting（包括密钥类设置）不会被意外暴露。

## 错误处理

| 情况 | HTTP 状态 | `data.alvin` | 行为 |
| --- | ---: | ---: | --- |
| DB 值为 `true` | 200 | `true` | 返回配置值 |
| DB 值为 `false` | 200 | `false` | 返回配置值 |
| 值带空格或大小写差异 | 200 | 解析结果 | 规范化后解析 |
| 记录缺失、空值或非法值 | 200 | `true` | 使用安全默认值 |
| 数据库查询失败 | 500 | 不返回 | 使用统一错误 envelope |

## 测试策略

### Service 单元测试

为专用读取方法使用 repository stub，覆盖：

- `"true"`、`"TRUE"` 和带空格值返回 `true`。
- `"false"`、`"FALSE"` 和带空格值返回 `false`。
- 记录不存在、空字符串、`"1"` 及任意非法字符串回退 `true`。
- repository 数据库错误原样向上返回。

### 默认值初始化测试

- 新数据库初始化包含 `alvin=true`。
- 已有数据库缺少 `alvin` 时补齐 `true`。
- 已有 `alvin=false` 时不覆盖。
- 部分初始化数据库和并发插入场景中的 `alvin=false` 均不覆盖。
- 原子补齐默认值失败时初始化返回错误。

### Handler 与路由测试

- handler 返回 HTTP 200 和精确的统一 envelope。
- DB 中为 `false` 时 JSON 中是布尔值 `false`，不是字符串。
- handler 在数据库错误时返回 HTTP 500。
- 响应包含 `Cache-Control: no-store`。
- 路由测试验证 `GET /api/v1/settings/alvin` 无认证可访问。

## 运维修改方式

启动补齐默认记录后，可直接修改数据库：

```sql
UPDATE settings
SET value = 'false', updated_at = NOW()
WHERE key = 'alvin';
```

恢复为 `true` 时把 `value` 改回 `'true'`。接口不使用应用层缓存，因此更新后的下一次请求即可读取新值。

## 非目标

- 不新增管理员 UI 或写接口。
- 不把 `alvin` 注入前端 HTML，也不加入完整公开设置响应。
- 不实现通用公共 setting 查询接口或动态 key 白名单。
- 不增加 Redis 或进程内缓存。
- 不修改任何与该布尔值无关的业务逻辑。
