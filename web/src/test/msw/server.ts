import { setupServer } from 'msw/node';
import { http, HttpResponse } from 'msw';
// Existing page tests exercise the verified legacy display branch. IAM tests override this contract.
export const server = setupServer(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'legacy', legacy_admin: true, session: { user_id: '7', session_id: 'test-session', activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' } })));
