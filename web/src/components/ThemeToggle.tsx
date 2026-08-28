import { Moon, Sun } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { usePreference } from '@/hooks/usePreference';
import { t } from '@/lib/i18n';

type Theme = 'light' | 'dark';

function getInitialTheme(): Theme {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark');
}

export function ThemeToggle() {
  const [systemTheme] = useState<Theme>(getInitialTheme);
  const [theme, setTheme] = usePreference<Theme>('theme', systemTheme);

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  const nextTheme = theme === 'dark' ? 'light' : 'dark';
  const nextThemeLabel = nextTheme === 'dark' ? t('切换至深色模式') : t('切换至浅色模式');

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="rounded-full"
      aria-label={nextThemeLabel}
      title={nextThemeLabel}
      onClick={() => setTheme(nextTheme)}
    >
      {theme === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
    </Button>
  );
}
