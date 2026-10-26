import { useContext } from 'react';
import { PasswordRecoveryFlowContext } from '@/lib/password-recovery-flow';

export function usePasswordRecoveryFlow() {
  const flow = useContext(PasswordRecoveryFlowContext);
  if (!flow) throw new Error('Password recovery requires its flow provider');
  return flow;
}
