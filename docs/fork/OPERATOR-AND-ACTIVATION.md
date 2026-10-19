# Minimal companion to CLIProxyAPI

Run native CLIProxyAPI unchanged. This fork's `cmd/cpa-operator` adds a small public
account view with per-account window-start controls. It uses only Go's standard
library: no Manager database, collectors, credential copies, or React build.
The full native CLIProxyAPI admin UI is forwarded only through the private
listener, including banked-reset support offered by that UI.

```
cd apps/manager-server
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o cpa-operator ./cmd/cpa-operator
CPA_URL=http://127.0.0.1:18317 \
CPA_KEY_FILE=/private/existing-cpa-management-key \
CPA_ADMIN_ADDR=127.0.0.1:18330 \
CPA_OPERATOR_ADDR=127.0.0.1:18331 \
CPA_OPERATOR_PASSWORD_FILE=/private/operator-password \
CPA_ACTIVATION_FILE=/private/window-activation.json ./cpa-operator
```

All listeners and the CPA upstream must use loopback IPs. Publish only the
operator listener through the public HTTPS reverse proxy. Admin is Tailscale
Serve only; never publish it through the internet gateway or Funnel.

## Public view

- `GET /`: login shell, containing no account data.
- `POST /api/login`: password only, no username; signed 12-hour HttpOnly,
  Secure, SameSite=Strict cookie. Password changes invalidate sessions.
- `POST /api/logout`: clear browser session.
- `GET /api/accounts`: short account names (email local part), opaque account
  IDs, quota and cached banked-reset count. No domains, OAuth data or file paths.
- `POST /api/account/status`: exactly `{id, disabled}`.
- `GET /api/windows`: per-account schedule state, first start, generated times, timezone,
  next run and last result, keyed only by opaque account IDs.
- `PUT /api/account/window`: exactly `{id, enabled, first_start}`; merges only this
  account's fixed Luna/low schedule and preserves other accounts and run records.
- `POST /api/account/window/start`: exactly `{id}`; checks live quota and
  starts only an expired five-hour window. Disabled accounts remain skipped.
- `POST /api/account/reset`: exactly `{id, request_id}`. `request_id` is a UUID.
  Posts one explicit redemption to the fixed upstream reset-consume endpoint
  using the selected account's existing CPA auth. No automatic resets/retries.
  A confirmed `reset` or `already_redeemed` also clears only that account's
  native CPA routing cooldown, so restored quota can be used immediately.
  A cooldown-clear failure reports that redemption succeeded and retains the
  UUID for a safe retry.
  The browser confirms spending, then reuses the same UUID after an uncertain
  response. Do not create a new attempt after a network error without checking
  quota. Actual upstream outcomes remain authoritative.

Mutations require same-origin JSON and reject unknown fields. Machine clients
may send the operator password as a Bearer token instead of a cookie. Neither
that password nor forged Tailscale headers grant administrator access. Quota
readings are cached for 30 seconds and invalidated after toggles/redemptions.
5-hour and weekly display columns use the actual window duration, including
accounts that expose only a weekly window.

## Private settings

- `/management.html`: original native CLIProxyAPI panel and management routes.
- `/activation/`: optional private scheduler form, authenticated by the existing CPA
  management key. This page keeps the key in memory only.

Schedules persist in an atomic 0600 JSON file, using account IDs already in CPA.
Set one first daily HH:MM start per account. Later starts follow at elapsed
5-hour-1-minute intervals until midnight in the configured IANA timezone; the
next day restarts from the chosen first time. Existing lists migrate from their
earliest time. No starts wrap into the following day. DST changes preserve the
elapsed interval; a first time skipped by a spring-forward transition skips that
day. Each generated slot checks live quota,
then sends a tiny `gpt-6-luna`/`low` prompt via the native CPA management API
using the selected auth_index and `$TOKEN$`. Active windows, disabled accounts,
weekly exhaustion, and accounts without a five-hour window are skipped.
Imminent resets may wait up to ten minutes. Missed slots are not caught up.
A persisted attempt record prevents duplicate requests across restart or DST.
No automatic reset credits are spent. Preserve the activation JSON when moving
from Manager mode; the standalone companion reads the same format/path.

## T3 hub compatibility

The public root hub still accepts the existing pool management key. It exposes
only redacted account metadata and two fixed GET quota/reset-credit endpoints.
It cannot download auth files, change configuration or proxy arbitrary URLs.
Credit consumption is a dedicated operator action, not a generic public proxy.

The former full Manager mode remains in this fork for development compatibility;
it is no longer needed for the deployment above.

## Automatic recovery after external quota resets

The companion reconciles cooldowns at startup and every minute, independently
of window-start schedules. It reads live provider quota only for enabled Codex
accounts whose native CPA state reports `usage_limit_reached`. The old future
retry deadline never prevents this recheck. No prompts, credits, account toggles,
or configuration changes are used to check recovery.

Recovery requires explicit `allowed: true`, `limit_reached: false`, valid usage
percentages below 100% in the reported primary/weekly windows, and healthy
additional model buckets when present. Missing/invalid data, failed quota reads,
real exhaustion and unrelated authentication/network errors do not authorize a
clear. The final native state is reread under the mutation lock so a user toggle
or newer 429 during the quota read cancels that recovery attempt.

Only the selected account's local cooldown is cleared via native reset-quota.
If requests repeatedly return 429 despite optimistic metadata, clear attempts
back off for 1, 2, 4, 8 and at most 15 minutes, while quota reads continue every
minute. Confirmed exhaustion resets the retry history, so a later external reset
is checked on the next tick. Healthy accounts are not continually quota-polled.
Retry state is memory-only; a companion restart performs a fresh quota check.
Successful recovery and clear failures are logged with opaque account IDs.
