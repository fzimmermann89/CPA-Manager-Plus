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
- `GET /api/windows`: per-account schedule state, daily times, timezone,
  next run and last result, keyed only by opaque account IDs.
- `PUT /api/account/window`: exactly `{id, enabled, times}`; merges only this
  account's fixed Luna/low schedule and preserves other accounts and run records.
- `POST /api/account/window/start`: exactly `{id}`; checks live quota and
  starts only an expired five-hour window. Disabled accounts remain skipped.
- `POST /api/account/reset`: exactly `{id, request_id}`. `request_id` is a UUID.
  Posts one explicit redemption to the fixed upstream reset-consume endpoint
  using the selected account's existing CPA auth. No automatic resets/retries.
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
Each selected daily HH:MM slot in the configured IANA timezone checks live quota,
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
