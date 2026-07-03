# Anthropic OAuth 默认模型映射

> **状态**: 已实现 · **日期**: 2026-07-03 · **分支**: `fix/anthropic-oauth-model-mapping` -> `dev`

---

## 背景

Web Chat 动态模型目录从默认 Anthropic/OpenAI 分组里的 active 账号 `credentials.model_mapping` 读取可用模型。模型广场再用 `/settings/model-catalog` 的公开展示目录与 `/chat/models` 的实际可用目录做交集,只展示当前 Web Chat 可调度的模型。

正式服排查时发现:

- `default_anthropic_group_id=3`
- 默认 Anthropic 分组里原生 Anthropic OAuth 账号 `cc` 为 active
- 该账号 `credentials.model_mapping` 为空
- Kiro 账号虽然有 Claude 映射,但处于 error 状态
- OpenAI 默认分组里的 Claude 映射会被 Web Chat family/provider 过滤,不会作为 Anthropic 模型展示

因此即使正式服已经配置了 Anthropic OAuth 账号,模型广场和 Web Chat 仍不会显示 Claude 模型。

## 目标

1. 编辑原生 Anthropic OAuth 账号时,必须能看到模型限制区域。
2. 如果账号已有 `credentials.model_mapping`,编辑页按已有配置回显,不能覆盖管理员配置。
3. 如果账号没有 `credentials.model_mapping`,编辑页默认选中前端已有的 Anthropic 支持模型列表。
4. 保存 Anthropic OAuth 账号时,必须把当前模型限制写回 `credentials.model_mapping`。
5. 复用现有 `ModelWhitelistSelector`、`getModelsByPlatform('anthropic')` 和 `buildModelMappingObject` 逻辑,不新增另一套模型源。

## 行为契约

### 无现有 mapping 的 Anthropic OAuth 账号

打开编辑弹窗时:

1. 显示模型限制区域。
2. `allowedModels` 初始化为 `getModelsByPlatform('anthropic')`。
3. `modelMappings` 初始化为空。
4. `modelRestrictionMode` 为 `whitelist`。

保存时写入 identity mapping:

```json
{
  "model_mapping": {
    "claude-sonnet-4-6": "claude-sonnet-4-6",
    "claude-opus-4-8": "claude-opus-4-8"
  }
}
```

实际 keys 以 `frontend/src/composables/useModelWhitelist.ts` 中的 `claudeModels` 为准。

### 已有 mapping 的 Anthropic OAuth 账号

打开编辑弹窗时:

1. 使用 `splitModelMappingObject` 解析已有 `credentials.model_mapping`。
2. identity mapping 回显到白名单。
3. 非 identity mapping 回显到映射列表。
4. 如果只有映射列表,自动切到 `mapping` 模式。

保存时按当前 UI 状态重新构造 `credentials.model_mapping`。

## 实现范围

| 文件 | 说明 |
|---|---|
| `frontend/src/components/account/EditAccountModal.vue` | Anthropic OAuth 纳入 OAuth 模型限制 UI;无 mapping 时默认加载 Anthropic 模型列表;保存时持久化 `model_mapping` |
| `frontend/src/components/account/__tests__/EditAccountModal.spec.ts` | 覆盖 Anthropic OAuth 无 mapping 时默认回填并保存 Claude 模型白名单 |

## 不在本次范围

1. 创建 Anthropic OAuth 账号流程暂不改。当前需求只要求编辑页面默认补齐模型列表。
2. 不修改后端动态目录逻辑。后端已经按默认 Anthropic 分组账号 `model_mapping` 生成 Web Chat Claude 目录。
3. 不修改生产数据库。管理员保存账号后,最多等待 Web Chat 目录 30 秒缓存过期即可生效。

## 验证

本次变更使用以下命令验证:

```bash
pnpm test:run src/components/account/__tests__/EditAccountModal.spec.ts
pnpm typecheck
git diff --check
```

测试覆盖点:

- Anthropic OAuth 编辑页无 `model_mapping` 时渲染模型白名单。
- 默认白名单等于 `claudeModels`。
- 提交后 payload 中 `credentials.model_mapping` 为 `claudeModels` 的 identity mapping。

## 维护注意事项

1. Anthropic 支持模型列表继续以 `useModelWhitelist.ts` 的 `claudeModels` 为来源。
2. Web Chat 展示依赖账号 `model_mapping` key,不是 upstream live list。
3. 如果后续补创建流程,应复用本次编辑页的默认加载与保存语义,避免创建出的 Anthropic OAuth 账号再次出现空 `model_mapping`。
