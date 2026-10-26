import { createContext } from 'react';

export interface RecoveryDraft {
  email: string;
  token: string;
  password: string;
  confirmation: string;
  complete: boolean;
}

export const EMPTY_RECOVERY_DRAFT: RecoveryDraft = { email: '', token: '', password: '', confirmation: '', complete: false };

export interface PasswordRecoveryFlow {
  draft: RecoveryDraft;
  update: (patch: Partial<RecoveryDraft>) => void;
  finish: () => void;
  isCurrent: () => boolean;
}

export const PasswordRecoveryFlowContext = createContext<PasswordRecoveryFlow | null>(null);
