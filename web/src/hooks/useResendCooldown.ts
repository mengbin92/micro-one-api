import { useEffect, useState } from 'react';

export function useResendCooldown() {
  const [deadline, setDeadline] = useState(0);
  const [seconds, setSeconds] = useState(0);

  useEffect(() => {
    if (!deadline) return;
    const interval = window.setInterval(() => {
      const remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      setSeconds(remaining);
      if (!remaining) window.clearInterval(interval);
    }, 1000);
    return () => window.clearInterval(interval);
  }, [deadline]);

  return {
    seconds,
    start: () => { setDeadline(Date.now() + 60_000); setSeconds(60); },
  };
}
