// IDs follow domain/upstream/provider/factory.go, shared by all admin views.
export const CHANNEL_PROVIDER_NAMES: Record<number, string> = {
  1: 'OpenAI',
  2: 'Anthropic',
  3: 'Gemini',
  4: 'Claude',
  5: 'Azure',
  6: 'DeepSeek',
  14: 'Hunyuan',
  23: 'OpenRouter',
  24: 'SiliconFlow',
  32: 'CodexOAuth',
  33: 'ClaudeOAuth',
  34: 'ZhipuPlan',
  35: 'MinimaxPlan',
  36: 'KimiOAuth',
};

// Native Hunyuan forwarding is not installed; display existing rows accurately
// without offering it as a new OpenAI-compatible channel.
export const CHANNEL_CREATABLE_PROVIDERS = Object.fromEntries(
  Object.entries(CHANNEL_PROVIDER_NAMES).filter(([id]) => id !== '14'),
);
