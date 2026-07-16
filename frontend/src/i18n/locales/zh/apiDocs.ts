export default {
  apiDocs: {
    title: 'API 文档',
    navLabel: 'API 文档',
    beginnerGuide: '新手教程',
    apiKeys: 'API 密钥',
    search: '搜索文档',
    searchPlaceholder: '搜索指南和 API 参考',
    searchCategories: {
      guide: '指南',
      endpoint: 'API 参考',
      platform: '平台'
    },
    noResults: '没有匹配的文档页面。',
    menu: '文档菜单',
    onThisPage: '本页内容',
    copy: '复制',
    copied: '已复制',
    dashboard: '控制台',
    login: '登录',
    notFoundTitle: '未找到文档页面',
    notFoundDescription: '请求的文档页面不存在。',
    nav: {
      quickstart: '快速开始',
      clients: '客户端集成',
      reference: 'API 参考',
      advanced: '高级能力',
      platform: '平台'
    },
    pages: {
      quickstart: {
        title: '快速开始',
        summary: '完成身份验证、选择基础地址并发送第一个请求。'
      },
      authentication: {
        title: '身份验证',
        summary: '通过支持的请求头发送统一 API 密钥。'
      },
      clientIntegration: {
        title: '客户端集成',
        summary: '为网关配置受支持的命令行客户端和开发工具包。'
      },
      capabilities: {
        title: '能力',
        summary: '在受支持时使用流式响应、工具、结构化输出、推理和提示词缓存。'
      },
      messages: {
        title: '消息',
        summary: '创建 Anthropic 兼容的消息响应。'
      },
      countTokens: {
        title: '计算令牌',
        summary: '在创建消息前估算输入令牌数。'
      },
      responses: {
        title: '响应',
        summary: '根据文本或结构化输入创建 OpenAI 兼容响应。'
      },
      chatCompletions: {
        title: '聊天补全',
        summary: '为兼容客户端创建聊天补全。'
      },
      models: {
        title: '模型',
        summary: '列出当前 API 密钥可用的模型。'
      },
      imageGenerations: {
        title: '图像生成',
        summary: '根据文本提示词生成图像。'
      },
      imageEdits: {
        title: '图像编辑',
        summary: '使用文本指令编辑上传的图像。'
      },
      errors: {
        title: '错误',
        summary: '识别网关和协议错误结构，并采取建议的处理措施。'
      },
      requestId: {
        title: '请求 ID',
        summary: '记录用于问题排查和支持的关联信息。'
      },
      keySecurity: {
        title: 'API 密钥安全',
        summary: '通过有效期、额度、速率周期和网络规则保护密钥。'
      }
    },
    sections: {
      overview: '概述',
      authentication: '身份验证',
      request: '请求',
      parameters: '参数',
      response: '响应',
      streaming: '流式响应',
      errors: '错误',
      troubleshooting: '问题排查',
      installation: '安装',
      configuration: '配置',
      security: '安全'
    },
    guideSectionTitles: {
      quickstart: {
        baseUrl: '基础地址',
        apiKey: 'API 密钥',
        firstRequest: '第一个请求',
        availableModels: '可用模型'
      },
      authentication: {
        bearer: 'Bearer 身份验证',
        xApiKey: 'x-api-key 身份验证',
        keySafety: '密钥安全',
        deprecatedQuery: '查询字符串凭证'
      },
      clientIntegration: {
        claudeCode: 'Claude Code 配置',
        codexCli: 'Codex CLI 配置',
        opencode: 'OpenCode 配置',
        ccSwitch: 'CC Switch 配置',
        pythonSdk: 'Python SDK 示例'
      },
      capabilities: {
        streaming: '流式响应',
        tools: '工具调用',
        structuredOutput: '结构化输出',
        reasoning: '推理控制',
        promptCache: '提示词缓存'
      },
      errors: {
        gatewayEnvelope: '网关错误结构',
        gatewayCodes: '网关错误代码',
        anthropicEnvelope: 'Anthropic 错误结构',
        openaiEnvelope: 'OpenAI 错误结构',
        streamErrors: '流式错误'
      },
      requestId: {
        headers: '关联请求头',
        supportChecklist: '支持信息清单',
        redaction: '诊断信息脱敏'
      },
      keySecurity: {
        expiration: '密钥有效期',
        quota: '密钥额度',
        rateWindows: '速率周期',
        ipRules: 'IP 与 CIDR 规则'
      }
    },
    labels: {
      required: '必填',
      optional: '可选',
      type: '类型',
      parameter: '参数',
      protocol: '协议',
      curl: 'cURL',
      python: 'Python',
      successExample: '成功响应',
      streamExample: '流式事件'
    },
    tables: {
      code: '代码',
      recommendedAction: '建议操作',
      window: '周期',
      rule: '规则',
      matchingIpCidr: '匹配的 IP/CIDR',
      whitelist: '白名单',
      blacklist: '黑名单',
      allowed: '仅允许匹配项',
      denied: '拒绝匹配项'
    },
    parameters: {
      model: '本次请求选择的模型。',
      maxTokens: '最多生成的令牌数量。',
      messages: '按顺序发送给模型的对话消息。',
      stream: '是否返回增量事件。',
      tools: '模型可以调用的工具定义。',
      system: '随对话提供的系统指令。',
      input: '用于生成响应的文本或结构化输入。',
      text: '响应的文本输出配置。',
      reasoning: '所选模型支持的推理控制项。',
      responseFormat: '请求的结构化响应格式。',
      prompt: '用于生成或编辑图像的文本指令。',
      size: '请求的输出图像尺寸。',
      n: '返回的图像数量。',
      quality: '请求的图像质量设置。',
      image: '需要编辑的源图像文件。'
    },
    guides: {
      quickstart: {
        intro: '一个密钥可以验证文档所列 Anthropic 和 OpenAI 兼容接口的请求。',
        baseUrl: '使用 LINX2 显示的基础地址，并由共享示例追加端点路径。',
        apiKey: '复制示例前，请先创建或选择一个密钥。',
        firstRequest: '先发送一个简短的文本请求，再按对应协议处理返回结构。',
        models: '使用同一个密钥获取模型列表，不要假定所有模型都可用。',
        beginnerGuide: '通过新手教程完成交互式客户端配置。',
        apiKeys: '前往 API 密钥创建、轮换、禁用或替换凭证。'
      },
      authentication: {
        intro: '每个已记录的端点都需要有效的统一密钥。',
        bearer: 'Bearer 身份验证适用于文档所列的网关接口。',
        xApiKey: 'Anthropic 兼容请求也接受专用密钥请求头。',
        safety: '请将密钥存入环境或密钥管理器，切勿提交到源代码仓库。',
        deprecatedQuery: '不要在查询字符串中发送凭证，请将其移到请求头。'
      },
      clientIntegration: {
        intro: '使用共享客户端构建器，使基础地址和示例模型与 LINX2 其他位置保持一致。',
        installation: '请先从已验证的来源安装客户端，再应用网关设置。',
        configuration: '根据操作系统和所选协议使用生成的配置。',
        macosNote: 'macOS 安装与配置',
        windowsNote: 'Windows 安装与配置替代方案'
      },
      capabilities: {
        intro: '可用能力取决于所选模型以及各端点记录的协议字段。',
        streaming: '流式响应会返回增量协议事件，客户端应持续处理直至结束。',
        tools: '工具调用允许模型请求应用定义的操作，具体操作由应用执行。',
        structuredOutput: '结构化输出将兼容响应约束为请求的格式。',
        reasoning: '只有所选模型接受时，推理控制项才会被转发。',
        promptCache: '当所选路由支持文档所列控制项时，提示词缓存可以减少重复上下文处理。'
      },
      requestId: {
        intro: '关联请求头可帮助支持人员定位单次请求，同时无需暴露凭证。',
        headers: '如果响应中包含两个关联值，请全部记录。',
        supportChecklist: '提交支持请求时，请附上端点、时间戳、关联值和脱敏后的错误响应。',
        redaction: '分享诊断信息前，请移除 API 密钥、服务商凭证和敏感提示内容。'
      },
      keySecurity: {
        intro: '请将每个密钥视为机密信息，并只授予客户端所需的访问范围。',
        expiration: '设置有效期，并在到期前轮换密钥。',
        quota: '使用额度限制单个密钥可消耗的总量。',
        rateWindows: '配置短期和长期用量周期，限制一段时间内的消耗。',
        ipRules: '谨慎使用网络允许或拒绝规则，并从目标客户端网络测试访问。'
      },
      errors: {
        gatewayEnvelope: '身份验证和计费检查可能在协议处理开始前返回网关错误结构。',
        protocolEnvelope: '参数验证错误使用所选协议对应的错误结构。',
        streamFailures: '流式响应开始后，除了初始 HTTP 响应，还应检查终止事件。'
      }
    },
    errors: {
      gatewayEnvelopeWarning: '如果身份验证或计费检查先失败，OpenAI 兼容端点也可能返回网关错误结构。',
      actions: {
        api_key_in_query_deprecated: '将密钥移到请求头。',
        API_KEY_REQUIRED: '添加 Bearer 或专用密钥请求头身份验证。',
        INVALID_API_KEY: '检查密钥或重新创建密钥。',
        API_KEY_DISABLED: '启用或替换密钥。',
        USER_NOT_FOUND: '联系管理员检查密钥归属。',
        USER_INACTIVE: '恢复用户账号。',
        ACCESS_DENIED: '检查密钥的网络访问规则。',
        API_KEY_EXPIRED: '延长有效期或替换密钥。',
        GROUP_DELETED: '将密钥绑定到可用分组。',
        GROUP_DISABLED: '将密钥绑定到启用的分组。',
        GROUP_NOT_ALLOWED: '申请访问权限或绑定其他分组。',
        INSUFFICIENT_BALANCE: '充值或检查订阅覆盖情况。',
        SUBSCRIPTION_INVALID: '检查订阅状态。',
        API_KEY_QUOTA_EXHAUSTED: '提高或重置密钥额度。',
        USAGE_LIMIT_EXCEEDED: '等待周期重置或修改已配置的限制。',
        INTERNAL_ERROR: '报告请求关联值。',
        SUBSCRIPTION_MAINTENANCE_FAILED: '报告请求关联值。'
      }
    }
  }
}
