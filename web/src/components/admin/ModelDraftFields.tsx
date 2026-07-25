import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  PROVIDER_OPTIONS,
  TYPE_OPTIONS,
  TIER_OPTIONS,
  validateMetadata,
  type ModelDraft,
} from '@/lib/model-draft';

export function ModelDraftFields({
  draft,
  onChange,
  isEdit,
}: {
  draft: ModelDraft;
  onChange: (patch: Partial<ModelDraft>) => void;
  isEdit?: boolean;
}) {
  const metadataError = validateMetadata(draft.metadata);
  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-2 gap-4">
        <div className="grid gap-2">
          <Label htmlFor="model-id">模型 ID</Label>
          <Input
            id="model-id"
            value={draft.modelId}
            disabled={isEdit}
            onChange={(e) => onChange({ modelId: e.target.value })}
            placeholder="如 gpt-4o, claude-3-5-sonnet"
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="display-name">显示名称</Label>
          <Input
            id="display-name"
            value={draft.displayName}
            onChange={(e) => onChange({ displayName: e.target.value })}
            placeholder="如 GPT-4o"
          />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="grid gap-2">
          <Label htmlFor="provider">提供商</Label>
          <select
            id="provider"
            value={draft.provider}
            onChange={(e) => onChange({ provider: e.target.value })}
            className="h-8 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm"
          >
            {PROVIDER_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </div>
        <div className="grid gap-2">
          <Label htmlFor="model-type">模型类型</Label>
          <select
            id="model-type"
            value={draft.modelType}
            onChange={(e) => onChange({ modelType: e.target.value })}
            className="h-8 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm"
          >
            {TYPE_OPTIONS.filter((o) => o.value).map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="grid gap-2">
          <Label htmlFor="context-window">上下文窗口</Label>
          <Input
            id="context-window"
            type="number"
            value={draft.contextWindow}
            onChange={(e) => onChange({ contextWindow: e.target.value })}
            placeholder="如 128000"
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="tier">等级</Label>
          <select
            id="tier"
            value={draft.tier}
            onChange={(e) => onChange({ tier: e.target.value })}
            className="h-8 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm"
          >
            {TIER_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="grid gap-2">
          <Label htmlFor="pricing-input">输入价格 (/1K tokens)</Label>
          <Input
            id="pricing-input"
            type="number"
            step="0.000001"
            value={draft.pricingInput}
            onChange={(e) => onChange({ pricingInput: e.target.value })}
            placeholder="如 0.005"
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="pricing-output">输出价格 (/1K tokens)</Label>
          <Input
            id="pricing-output"
            type="number"
            step="0.000001"
            value={draft.pricingOutput}
            onChange={(e) => onChange({ pricingOutput: e.target.value })}
            placeholder="如 0.015"
          />
        </div>
      </div>
      <div className="grid gap-2">
        <Label htmlFor="category">分类</Label>
        <Input
          id="category"
          value={draft.category}
          onChange={(e) => onChange({ category: e.target.value })}
          placeholder="如 large-language, image, audio"
        />
      </div>
      <div className="grid gap-2">
        <Label htmlFor="description">描述</Label>
        <Input
          id="description"
          value={draft.description}
          onChange={(e) => onChange({ description: e.target.value })}
          placeholder="模型描述"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="grid gap-2">
          <Label htmlFor="capabilities">能力标签 (逗号分隔)</Label>
          <Input
            id="capabilities"
            value={draft.capabilities}
            onChange={(e) => onChange({ capabilities: e.target.value })}
            placeholder="如 vision, function_calling, streaming"
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="tags">自定义标签 (逗号分隔)</Label>
          <Input
            id="tags"
            value={draft.tags}
            onChange={(e) => onChange({ tags: e.target.value })}
            placeholder="如 large-context, fast"
          />
        </div>
      </div>
      <div className="grid gap-2">
        <Label htmlFor="metadata">元数据 (JSON)</Label>
        <Input
          id="metadata"
          value={draft.metadata}
          onChange={(e) => onChange({ metadata: e.target.value })}
          placeholder="如 JSON 字符串"
          aria-invalid={!!metadataError}
        />
        {metadataError && (
          <p className="text-xs text-destructive">{metadataError}</p>
        )}
      </div>
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={draft.isPublic}
          onChange={(e) => onChange({ isPublic: e.target.checked })}
          className="size-4 rounded border-input"
        />
        <span className="text-sm">公开显示给用户</span>
      </label>
    </div>
  );
}
