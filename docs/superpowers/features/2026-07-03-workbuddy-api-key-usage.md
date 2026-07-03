# WorkBuddy 使用密钥配置示例

> **状态**: 已实现 · **日期**: 2026-07-03 · **分支**: `dev`

---

## 背景

正式服近期出现 WorkBuddy 调用转发网关时的 404。排查方向是 WorkBuddy 自定义模型配置里的模型名称和网关实际支持的模型 ID 不一致:客户端可能把展示名作为 `model` 传给 OpenAI-compatible `/chat/completions` 接口。

WorkBuddy 文档说明自定义模型通过 `~/.workbuddy/models.json` 或 Windows `%userprofile%\.workbuddy\models.json` 配置,每个模型需要提供 `id`、`name`、`vendor`、`url`、`apiKey` 等字段;OpenAI 兼容模式下 `url` 应指向完整的 `/chat/completions` 地址。

## 目标

1. 在“使用 API 密钥”弹窗中新增 WorkBuddy 使用 Tab。
2. 给出当前转发网关可以直接使用的 WorkBuddy `models.json` 示例。
3. 示例中的 `url` 必须指向 `/v1/chat/completions`。
4. 示例中的 `id` 和 `name` 必须使用网关支持的真实模型 ID,避免 WorkBuddy 发送展示名。
5. Opus 默认示例使用当前已支持的 `claude-opus-4-8`,不要继续使用旧的 `claude-opus-4-7` 作为示例默认值。

## 行为契约

### Unified 密钥

WorkBuddy Tab 生成一个 `models.json`,包含:

```json
{
  "availableModels": ["gpt-5.5", "claude-sonnet-5", "claude-opus-4-8"]
}
```

`models` 中每个模型的 `id` 和 `name` 保持一致,例如:

```json
{
  "id": "claude-opus-4-8",
  "name": "claude-opus-4-8",
  "vendor": "Custom",
  "url": "https://example.com/v1/chat/completions",
  "apiKey": "sk-..."
}
```

### OpenAI 密钥

只展示 OpenAI 模型:

```json
{
  "availableModels": ["gpt-5.5"]
}
```

### Anthropic 类密钥

只展示 Claude 模型:

```json
{
  "availableModels": ["claude-sonnet-5", "claude-opus-4-8"]
}
```

## 实现范围

| 文件 | 说明 |
|---|---|
| `frontend/src/components/keys/UseKeyModal.vue` | 新增 WorkBuddy Tab、`models.json` 生成器、`/chat/completions` URL 规范化,并将 Anthropic Python SDK 示例默认 Opus 更新到 4.8 |
| `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts` | 覆盖 WorkBuddy Tab、配置路径、`availableModels`、`id/name` 一致性、URL 和 API Key |
| `frontend/src/i18n/locales/zh.ts` | 新增 WorkBuddy 中文文案 |
| `frontend/src/i18n/locales/en.ts` | 新增 WorkBuddy 英文文案 |

## 不在本次范围

1. 不修改后端转发逻辑。
2. 不修改正式服配置或生产数据库。
3. 不删除旧模型 `claude-opus-4-7` 的兼容支持;本次只调整使用示例默认值。

## 验证

本次变更使用以下命令验证:

```bash
npx vitest run src/components/keys/__tests__/UseKeyModal.spec.ts --testNamePattern "WorkBuddy"
npx vitest run src/components/keys/__tests__/UseKeyModal.spec.ts
npm run typecheck
npm run build
```

验证结果:

- WorkBuddy 定向单测通过。
- `UseKeyModal` 全量 11 个单测通过。
- 前端类型检查通过。
- 前端 production build 通过;仅保留已有 Browserslist 和 Vite chunk 警告。

## 维护注意事项

1. WorkBuddy 示例里的 `name` 不要改成 `Claude Opus 4.8` 这类展示名;保持和 `id` 相同,避免客户端把展示名作为 `model` 发给网关。
2. 如果默认推荐模型升级,同步更新 `workBuddyModelsForPlatform` 和对应单测。
3. `url` 应始终是完整 Chat Completions 地址,不要只填 `/v1` 基础路径。
