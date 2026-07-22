export interface ModelDraft {
  modelId: string;
  displayName: string;
  description: string;
  provider: string;
  modelType: string;
  contextWindow: string;
  pricingInput: string;
  pricingOutput: string;
  category: string;
  tier: string;
  isPublic: boolean;
  capabilities: string;
  tags: string;
  metadata: string;
}

export const emptyDraft: ModelDraft = {
  modelId: '',
  displayName: '',
  description: '',
  provider: '',
  modelType: 'chat',
  contextWindow: '',
  pricingInput: '',
  pricingOutput: '',
  category: '',
  tier: '',
  isPublic: true,
  capabilities: '',
  tags: '',
  metadata: '',
};

export const PROVIDER_OPTIONS = [
  { value: '', label: '—' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'gemini', label: 'Google Gemini' },
  { value: 'zhipu', label: '智谱' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'minimax', label: 'MiniMax' },
  { value: 'kimi', label: 'Kimi' },
  { value: 'siliconflow', label: 'SiliconFlow' },
  { value: 'openrouter', label: 'OpenRouter' },
];

export const TYPE_OPTIONS = [
  { value: '', label: '全部' },
  { value: 'chat', label: '对话' },
  { value: 'completion', label: '补全' },
  { value: 'embedding', label: '嵌入' },
  { value: 'image', label: '图像' },
];

export const TIER_OPTIONS = [
  { value: '', label: '—' },
  { value: 'entry', label: '入门' },
  { value: 'standard', label: '标准' },
  { value: 'premium', label: '高级' },
];

export const STATUS_OPTIONS = [
  { value: '', label: '全部' },
  { value: '0', label: '禁用' },
  { value: '1', label: '启用' },
  { value: '2', label: '测试中' },
];

export const CATEGORY_OPTIONS = [
  { value: '', label: '全部分类' },
  { value: 'large-language', label: '大语言模型' },
  { value: 'image', label: '图像' },
  { value: 'audio', label: '音频' },
  { value: 'embedding', label: '嵌入' },
];

export function splitCsv(value: string): string[] {
  return value ? value.split(',').map((s) => s.trim()).filter(Boolean) : [];
}

export function validateMetadata(metadata: string): string | null {
  if (!metadata.trim()) return null;
  try {
    JSON.parse(metadata);
    return null;
  } catch {
    return '元数据不是有效的 JSON 格式';
  }
}
