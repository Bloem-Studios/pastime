# Synthetic populated usability acceptance

Run against a freshly built, migrated app of your own (default
`http://localhost:18162`) and PostgreSQL database:

```sh
PASTIME_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:18543/pastime_test \
  pnpm exec playwright test --config tests/browser/usability.config.ts
```

Requirements: `psql`, installed Playwright Chromium, migrated database, and the
existing synthetic `e2e_owner` bootstrap account. The helper uses only the
synthetic password already defined in the existing browser tests. It never reads
`.env`, production snapshots, or provider credentials. Do not run against a live
service or development database. URL guards reject other database names/ports
and non-loopback apps. `PASTIME_USABILITY_BASE_URL` can select the task app's
18161 listener instead; the app's configured Origin must match exactly.

You provision the database and schema. If you run the app in an
isolated PostgreSQL schema, pass the same `options=-csearch_path=...` URL to this
suite. The helper operates in that connection's current schema; it does not
create/drop schemas or change the app connection. It creates one randomly named
member/library per Playwright worker, inserts uniquely identified synthetic
catalog metadata, and removes only that account/catalog tree at teardown. Tests stop after the first failure to avoid repeated account setup/login attempts.
They run with one worker to keep rate limits bounded and the populated baseline
stable. Account creation adds the existing synthetic owner as a **viewer of the
synthetic collaborative list only** to verify role boundaries; it never changes
that account's global permissions. Teardown removes the synthetic list with its
owner account. If the process is killed, the random `usability_` member and
`synthetic` provider records can remain in the test database.

Coverage: populated desktop/390/320 grid and table; manual and smart list
membership; durable manual progress/private notes; populated season episodes;
tier placement persistence; collaborative owner/viewer visibility and rejected
viewer write; anonymous unlisted-share contents and private field omission;
statistics with a timed episode; history and upcoming calendar release;
manual-entry advanced disclosure, focus trap, dirty cancel/discard and save;
disconnected integrations directory/detail separation; failed local recovery
with retained draft and identity-provider guidance.

Limits: short 480px viewports are layout proxies, not real OS virtual keyboards.
Recovery rejection is a deterministic intercepted response, not a real password
reset. Successful provider linking/import matching, linked-service failures,
provider browsing/filter/pagination contracts, public profile opt-in, token
rotation, editions/copies, all account roles and formal WCAG certification are
outside this file. There are no screenshots-only assertions or arbitrary sleeps.
Tests require the repair UI contracts (compact manual editor and task directory)
and intentionally fail if those behaviors regress.
