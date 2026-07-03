# Quota Removal Follow-up

This branch moves wallet balances, recharge amounts, redemptions, pricing costs,
and user-facing money displays to direct amount units with 4 decimal places.
Some `quota` names and compatibility surfaces intentionally remain so the
runtime behavior can change without a broad API and schema rename in the same
patch.

Status after the wallet API/frontend rename:

- Done: wallet-facing API payloads now use `balance`, `used_amount`,
  `frozen_amount`, `today_amount`, `new_balance`, and `price_amount` instead of
  quota terminology.
- Done: payment wallet orders now use the `balance` asset type. Subscription
  payment assets continue to use `subscription`.
- Done: the frontend amount formatter lives in `web/src/lib/amount.ts`, and
  user-facing wallet pages/types no longer depend on `web/src/lib/quota.ts`.
- Preserved: relay/OpenAI-compatible quota endpoints, channel usage fields,
  log/usage aggregate fields, reconciliation quota fields, subscription quota
  windows, `QuotaPerUnit`, and invitation quota config names.
- Still pending: the underlying database/model rename for wallet balance fields
  such as `users.quota`, `users.used_quota`, `users.frozen_quota`, and related
  repository method names.

Follow-up items:

1. [x] Rename wallet API fields and responses that still expose `quota` for money,
   including user dashboard/account snapshots, redeem/top-up compatibility
   responses, payment order asset type labels, and admin user balance payloads.
2. [ ] Rename database/model fields that represent wallet money but are still named
   `quota`, such as account balance snapshots, ledger amount columns, and payment
   order asset fields. No migration was added in this branch because the project
   is not live yet.
3. [ ] Split or remove compatibility config names that still use one-api quota
   terminology for wallet flows, such as invitation bonus env vars and
   `quota_per_unit` pricing option names.
4. [ ] Decide whether relay/OpenAI-compatible quota endpoints should stay as a
   separate compatibility concept. Current relay `PAYMENT_QUOTA_PER_UNIT` and
   raw quota endpoints were not changed because they are not wallet display
   paths.
5. [x] Rename frontend helper modules/types from quota-oriented names after the API
   fields are renamed, especially `web/src/lib/quota.ts` and page-local
   `quota` properties that now hold amount units.
6. [ ] Update product/admin documentation and screenshots after the API/schema naming
   cleanup, so docs no longer explain user balances in quota terms.
