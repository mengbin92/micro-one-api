import { ArrowRight, AudioLines, File, Image, Type, Video, type LucideIcon } from 'lucide-react';
import { t } from '@/lib/i18n';

// Rounded tinted badges match the compact visual language used by the model
// reference UI. Unknown modalities remain visible as text instead of being
// silently discarded.
const MODALITY_ICONS: Record<string, { Icon: LucideIcon; className: string }> = {
  text: { Icon: Type, className: 'bg-blue-50 text-blue-600 dark:bg-blue-950/60 dark:text-blue-400' },
  image: { Icon: Image, className: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-950/60 dark:text-emerald-400' },
  audio: { Icon: AudioLines, className: 'bg-violet-50 text-violet-600 dark:bg-violet-950/60 dark:text-violet-400' },
  video: { Icon: Video, className: 'bg-orange-50 text-orange-600 dark:bg-orange-950/60 dark:text-orange-400' },
  file: { Icon: File, className: 'bg-amber-50 text-amber-600 dark:bg-amber-950/60 dark:text-amber-400' },
};

const MODALITY_LABELS: Record<string, string> = {
  text: '文本',
  image: '图像',
  audio: '音频',
  video: '视频',
  file: '文件',
};

export function ModalityIcons({ modalities }: { modalities: string[] }) {
  if (modalities.length === 0) {
    return <span className="text-muted-foreground">—</span>;
  }
  return (
    <span className="flex items-center gap-1.5">
      {modalities.map((modality, index) => {
        const label = t(MODALITY_LABELS[modality] ?? modality);
        const entry = MODALITY_ICONS[modality];
        if (!entry) {
          return (
            <span
              key={`${modality}-${index}`}
              className="inline-flex h-7 items-center rounded-lg bg-muted px-2 text-xs text-muted-foreground"
              title={label}
            >
              {label}
            </span>
          );
        }
        const { Icon, className } = entry;
        return (
          <span
            key={`${modality}-${index}`}
            role="img"
            aria-label={label}
            title={label}
            className={`inline-flex size-7 items-center justify-center rounded-lg ${className}`}
          >
            <Icon aria-hidden="true" className="size-4" strokeWidth={2.3} />
          </span>
        );
      })}
    </span>
  );
}

export function ModalityFlow({
  inputModalities,
  outputModalities,
}: {
  inputModalities: string[];
  outputModalities: string[];
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <ModalityIcons modalities={inputModalities} />
      <ArrowRight aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <ModalityIcons modalities={outputModalities} />
    </span>
  );
}
