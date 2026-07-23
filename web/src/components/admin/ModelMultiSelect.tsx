import { useQuery } from '@tanstack/react-query';
import { Check, Search } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Input } from '@/components/ui/input';
import { listModels } from '@/lib/model-management';
import { splitCsv } from '@/lib/model-draft';

/**
 * ModelMultiSelect lets the user pick models from the model registry
 * (Sprint 3 integration). It renders a searchable checkbox list of all
 * enabled models from the registry and also accepts free-form model IDs
 * that are not yet in the registry (backward-compatible with the legacy
 * CSV text input).
 *
 * The component is controlled: the parent owns the CSV string value and
 * receives updates via onChange.
 */
export function ModelMultiSelect({
  value,
  onChange,
  placeholder = 'Search models...',
  maxheight = 'max-h-48',
}: {
  value: string;
  onChange: (csv: string) => void;
  placeholder?: string;
  maxheight?: string;
}) {
  const [search, setSearch] = useState('');

  // Fetch all enabled models from the registry (page 1, large page size).
  const { data: registryModels, isLoading } = useQuery({
    queryKey: ['admin-models', 'select'],
    queryFn: async () => {
      const resp = await listModels({ page: 1, page_size: 500, status: 1 });
      return resp.models ?? [];
    },
    staleTime: 60_000,
  });

  const selectedSet = useMemo(() => {
    const set = new Set<string>();
    for (const m of splitCsv(value)) {
      set.add(m);
    }
    return set;
  }, [value]);

  const filteredModels = useMemo(() => {
    const models = registryModels ?? [];
    if (!search.trim()) return models;
    const kw = search.toLowerCase();
    return models.filter(
      (m) =>
        m.model_id.toLowerCase().includes(kw) ||
        m.display_name.toLowerCase().includes(kw),
    );
  }, [registryModels, search]);

  // Models in the CSV that are not in the registry (manually entered).
  const customModels = useMemo(() => {
    const registryIds = new Set((registryModels ?? []).map((m) => m.model_id));
    return splitCsv(value).filter((m) => !registryIds.has(m));
  }, [value, registryModels]);

  const toggle = (modelId: string) => {
    const next = new Set(selectedSet);
    if (next.has(modelId)) {
      next.delete(modelId);
    } else {
      next.add(modelId);
    }
    onChange(Array.from(next).join(','));
  };

  return (
    <div className="space-y-2">
      <div className="relative">
        <Search className="absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={placeholder}
          className="pl-8"
        />
      </div>
      <div className={`overflow-y-auto rounded-lg border ${maxheight}`}>
        {isLoading ? (
          <div className="px-3 py-4 text-center text-sm text-muted-foreground">
            Loading models...
          </div>
        ) : filteredModels.length === 0 && customModels.length === 0 ? (
          <div className="px-3 py-4 text-center text-sm text-muted-foreground">
            {search ? 'No models match your search' : 'No models available'}
          </div>
        ) : (
          <div className="divide-y">
            {customModels.map((modelId) => (
              <ModelCheckboxRow
                key={`custom-${modelId}`}
                modelId={modelId}
                displayName={modelId}
                provider="custom"
                checked={selectedSet.has(modelId)}
                onToggle={toggle}
              />
            ))}
            {filteredModels.map((m) => (
              <ModelCheckboxRow
                key={`reg-${m.id}`}
                modelId={m.model_id}
                displayName={m.display_name}
                provider={m.provider}
                checked={selectedSet.has(m.model_id)}
                onToggle={toggle}
              />
            ))}
          </div>
        )}
      </div>
      {selectedSet.size > 0 && (
        <div className="text-xs text-muted-foreground">
          {selectedSet.size} model{selectedSet.size > 1 ? 's' : ''} selected
        </div>
      )}
    </div>
  );
}

function ModelCheckboxRow({
  modelId,
  displayName,
  provider,
  checked,
  onToggle,
}: {
  modelId: string;
  displayName: string;
  provider: string;
  checked: boolean;
  onToggle: (modelId: string) => void;
}) {
  return (
    <div
      role="checkbox"
      aria-checked={checked}
      tabIndex={0}
      className="flex cursor-pointer items-center gap-2 px-3 py-2 hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:outline-none"
      onClick={() => onToggle(modelId)}
      onKeyDown={(e) => {
        if (e.key === ' ' || e.key === 'Enter') {
          e.preventDefault();
          onToggle(modelId);
        }
      }}
    >
      <div
        className={`flex size-4 shrink-0 items-center justify-center rounded border ${
          checked
            ? 'border-primary bg-primary text-primary-foreground'
            : 'border-input'
        }`}
      >
        {checked && <Check className="size-3" />}
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium">{displayName}</div>
        <div className="truncate text-xs text-muted-foreground">
          {modelId}
          {provider ? ` · ${provider}` : ''}
        </div>
      </div>
    </div>
  );
}
