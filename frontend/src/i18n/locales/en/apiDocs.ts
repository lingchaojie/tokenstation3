export default {
  apiDocs: {
    title: 'API Docs',
    navLabel: 'API Docs',
    beginnerGuide: 'Beginner Guide',
    apiKeys: 'API Keys',
    search: 'Search documentation',
    searchPlaceholder: 'Search guides and API reference',
    searchCategories: {
      guide: 'Guide',
      endpoint: 'API reference',
      platform: 'Platform'
    },
    noResults: 'No documentation pages matched your search.',
    menu: 'Documentation menu',
    onThisPage: 'On this page',
    copy: 'Copy',
    copied: 'Copied',
    dashboard: 'Dashboard',
    login: 'Log in',
    notFoundTitle: 'Documentation page not found',
    notFoundDescription: 'The requested documentation page does not exist.',
    nav: {
      quickstart: 'Quickstart',
      clients: 'Client integration',
      reference: 'API reference',
      advanced: 'Advanced capabilities',
      platform: 'Platform'
    },
    pages: {
      quickstart: {
        title: 'Quickstart',
        summary: 'Authenticate, choose a Base URL, and make your first request.'
      },
      authentication: {
        title: 'Authentication',
        summary: 'Send a unified API key in a supported request header.'
      },
      clientIntegration: {
        title: 'Client integration',
        summary: 'Configure supported command-line clients and SDKs for the gateway.'
      },
      capabilities: {
        title: 'Capabilities',
        summary: 'Use streaming, tools, structured output, reasoning, and prompt caching when supported.'
      },
      messages: {
        title: 'Messages',
        summary: 'Create an Anthropic-compatible message response.'
      },
      countTokens: {
        title: 'Count tokens',
        summary: 'Estimate input tokens before creating a message.'
      },
      responses: {
        title: 'Responses',
        summary: 'Create an OpenAI-compatible response from text or structured input.'
      },
      chatCompletions: {
        title: 'Chat Completions',
        summary: 'Create a chat completion for compatible clients.'
      },
      models: {
        title: 'Models',
        summary: 'List the models available to the current API key.'
      },
      imageGenerations: {
        title: 'Image generations',
        summary: 'Generate an image from a text prompt.'
      },
      imageEdits: {
        title: 'Image edits',
        summary: 'Edit an uploaded image with a text instruction.'
      },
      errors: {
        title: 'Errors',
        summary: 'Recognize gateway and protocol error envelopes and take the recommended action.'
      },
      requestId: {
        title: 'Request IDs',
        summary: 'Capture correlation values for troubleshooting and support.'
      },
      keySecurity: {
        title: 'API key security',
        summary: 'Protect keys with expiration, quotas, rate windows, and network rules.'
      }
    },
    sections: {
      overview: 'Overview',
      authentication: 'Authentication',
      request: 'Request',
      parameters: 'Parameters',
      response: 'Response',
      streaming: 'Streaming',
      errors: 'Errors',
      troubleshooting: 'Troubleshooting',
      installation: 'Installation',
      configuration: 'Configuration',
      security: 'Security'
    },
    guideSectionTitles: {
      quickstart: {
        baseUrl: 'Base URL',
        apiKey: 'API key',
        firstRequest: 'First request',
        availableModels: 'Available models'
      },
      authentication: {
        bearer: 'Bearer authentication',
        xApiKey: 'x-api-key authentication',
        keySafety: 'Key safety',
        deprecatedQuery: 'Query-string credentials'
      },
      clientIntegration: {
        claudeCode: 'Claude Code setup',
        codexCli: 'Codex CLI setup',
        opencode: 'OpenCode setup',
        ccSwitch: 'CC Switch setup',
        pythonSdk: 'Python SDK examples'
      },
      capabilities: {
        streaming: 'Streaming responses',
        tools: 'Tool calling',
        structuredOutput: 'Structured output',
        reasoning: 'Reasoning controls',
        promptCache: 'Prompt caching'
      },
      errors: {
        gatewayEnvelope: 'Gateway error envelope',
        gatewayCodes: 'Gateway error codes',
        anthropicEnvelope: 'Anthropic error envelope',
        openaiEnvelope: 'OpenAI error envelope',
        streamErrors: 'Streaming errors'
      },
      requestId: {
        headers: 'Correlation headers',
        supportChecklist: 'Support checklist',
        redaction: 'Diagnostic redaction'
      },
      keySecurity: {
        expiration: 'Key expiration',
        quota: 'Key quota',
        rateWindows: 'Rate windows',
        ipRules: 'IP and CIDR rules'
      }
    },
    labels: {
      required: 'Required',
      optional: 'Optional',
      type: 'Type',
      parameter: 'Parameter',
      protocol: 'Protocol',
      curl: 'cURL',
      python: 'Python',
      successExample: 'Success response',
      streamExample: 'Stream events'
    },
    tables: {
      code: 'Code',
      recommendedAction: 'Recommended action',
      window: 'Window',
      rule: 'Rule',
      matchingIpCidr: 'Matching IP/CIDR',
      whitelist: 'Whitelist',
      blacklist: 'Blacklist',
      allowed: 'Only matching entries are allowed',
      denied: 'Matching entries are denied'
    },
    parameters: {
      model: 'The model selected for the request.',
      maxTokens: 'The maximum number of tokens to generate.',
      messages: 'The ordered conversation messages sent to the model.',
      stream: 'Whether to return incremental events.',
      tools: 'Tool definitions the model may call.',
      system: 'System instructions supplied with the conversation.',
      input: 'Text or structured input for the response.',
      text: 'Text-output configuration for the response.',
      reasoning: 'Reasoning controls supported by the selected model.',
      responseFormat: 'The requested structured response format.',
      prompt: 'The text instruction used to create or edit an image.',
      size: 'The requested output image dimensions.',
      n: 'The number of images to return.',
      quality: 'The requested image quality setting.',
      image: 'The source image file to edit.'
    },
    guides: {
      quickstart: {
        intro: 'A single key can authenticate requests to the documented Anthropic- and OpenAI-compatible interfaces.',
        baseUrl: 'Use the Base URL shown by LINX2 and let the shared examples append the endpoint path.',
        apiKey: 'Create or select a key before copying an example.',
        firstRequest: 'Start with a small text request, then handle the returned protocol envelope.',
        models: 'Retrieve the model list with the same key instead of assuming every model is available.',
        beginnerGuide: 'Use Beginner Guide for an interactive client setup walkthrough.',
        apiKeys: 'Open API Keys to create, rotate, disable, or replace a credential.'
      },
      authentication: {
        intro: 'Every documented endpoint requires a valid unified key.',
        bearer: 'Bearer authentication works across the documented gateway interfaces.',
        xApiKey: 'The dedicated key header is also accepted for Anthropic-compatible requests.',
        safety: 'Store secrets in an environment or secret manager and never place them in source control.',
        deprecatedQuery: 'Do not send credentials in a query string; move them to a request header.'
      },
      clientIntegration: {
        intro: 'Use the shared client builders so Base URLs and example models match the rest of LINX2.',
        installation: 'Install each client from its verified source before applying gateway settings.',
        configuration: 'Use the generated configuration for your operating system and chosen protocol.',
        macosNote: 'macOS installation and configuration',
        windowsNote: 'Windows installation and configuration alternative'
      },
      capabilities: {
        intro: 'Capabilities depend on the selected model and the protocol fields documented for each endpoint.',
        streaming: 'Streaming returns incremental protocol events that clients must consume through completion.',
        tools: 'Tool calling lets a model request application-defined operations; the application executes them.',
        structuredOutput: 'Structured output constrains compatible responses to a requested shape.',
        reasoning: 'Reasoning controls are forwarded only where the selected model accepts them.',
        promptCache: 'Prompt caching may reduce repeated context processing when the selected route supports the documented controls.'
      },
      requestId: {
        intro: 'Correlation headers help support locate one request without exposing credentials.',
        headers: 'Record both response correlation values when they are present.',
        supportChecklist: 'Include the endpoint, timestamp, correlation values, and a sanitized error response in a support request.',
        redaction: 'Remove API keys, provider credentials, and sensitive prompt content before sharing diagnostics.'
      },
      keySecurity: {
        intro: 'Treat each key as a secret and grant only the access its client needs.',
        expiration: 'Set an expiration date and rotate the key before it expires.',
        quota: 'Use a quota to bound the total amount a key may consume.',
        rateWindows: 'Configure short and long usage windows to bound consumption over time.',
        ipRules: 'Use network allow or deny rules carefully, and test access from the intended client network.'
      },
      errors: {
        gatewayEnvelope: 'Authentication and billing checks can return a gateway envelope before protocol handling begins.',
        protocolEnvelope: 'Validation errors use the envelope of the selected protocol.',
        streamFailures: 'After streaming starts, inspect terminal events as well as the initial HTTP response.'
      }
    },
    errors: {
      gatewayEnvelopeWarning: 'An OpenAI-compatible endpoint can return a gateway error envelope when authentication or billing checks fail first.',
      actions: {
        api_key_in_query_deprecated: 'Move the key into a request header.',
        API_KEY_REQUIRED: 'Add Bearer or dedicated key-header authentication.',
        INVALID_API_KEY: 'Verify or recreate the key.',
        API_KEY_DISABLED: 'Enable or replace the key.',
        USER_NOT_FOUND: 'Contact the administrator about key ownership.',
        USER_INACTIVE: 'Restore the user account.',
        ACCESS_DENIED: 'Check the key network access rules.',
        API_KEY_EXPIRED: 'Extend or replace the key.',
        GROUP_DELETED: 'Bind the key to an available group.',
        GROUP_DISABLED: 'Bind the key to an active group.',
        GROUP_NOT_ALLOWED: 'Request access or bind another group.',
        INSUFFICIENT_BALANCE: 'Add balance or check subscription coverage.',
        SUBSCRIPTION_INVALID: 'Check the subscription state.',
        API_KEY_QUOTA_EXHAUSTED: 'Increase or reset the key quota.',
        USAGE_LIMIT_EXCEEDED: 'Wait for reset or change the configured limit.',
        INTERNAL_ERROR: 'Report the request correlation values.',
        SUBSCRIPTION_MAINTENANCE_FAILED: 'Report the request correlation values.'
      }
    }
  }
}
