# Shared Codex accounts: private owns OAuth, job uses access credentials

The native CLIProxyAPI binary is unchanged. `sync.py` projects exactly the
configured identities from private into job once per second. It copies access
tokens and identifying/expiry metadata, **never refresh tokens**, including
nested SIWC refresh credentials. Job files are independent mode-0600 files,
replaced atomically when credentials change, so the native watcher reloads them.
No symlinks, refresh-token duplication, cron requests, extra OAuth sessions, or
inference during synchronization. Private remains the sole token refresh owner.

Example deployment configuration (mode 0600, outside either auth directory):

```json
{
  "source": "/home/USER/etc/cliproxyapi/privat/auth",
  "destination": "/home/USER/etc/cliproxyapi/job/auth",
  "accounts": {"fz": "fz@example.com", "rp": "rp@example.com"}
}
```

Run as `python3 -u sync.py config.json` under Supervisor, with `umask=077` and
automatic restart. `--once` performs a single reconciliation. The process lock
prevents duplicate synchronizers. Missing, malformed, or ambiguous owner
identities remove their corresponding job projections. Other private accounts
are never copied. Other job files are not managed by this script; start with an
empty job auth directory and keep credential administration in private.

Set `CPA_AUTH_OWNER_URL=https://api.zimf.de/privat/` on the job companion. Its
public login controls link to private; its device-start API rejects independent
logins. The Tailscale admin proxy also blocks credential writes/login initiation
for the follower. Native loopback ports remain deployment-owned interfaces.

Keep fz and rp enabled in private so native automatic refresh can run. Job's
enabled/disabled settings remain independent. Existing job API/management keys
remain distinct. Usage, quotas and banked resets belong to the upstream account,
so private and job share them; this does not create another allocation.
Configure window schedules in one pool only to avoid duplicate wake-up requests.
Job starts with no schedules enabled.

If synchronization is interrupted, job retains its last access tokens until
their expiry; Supervisor restarts the synchronizer. Private never depends on
job. After owner reauthorization/rotation, job reloads the new access token on
the next successful reconciliation (normally within a couple of seconds).

Verification:

```sh
python3 -m unittest discover -s deploy/shared-codex-auth -v
cd apps/manager-server
go test -race ./internal/operator ./cmd/cpa-operator
```

Rollback: stop the synchronizer, remove `shared-codex-fz.json` and
`shared-codex-rp.json` from job, restore the job companion Supervisor backup and
restart that companion. Private credentials and the private proxy are untouched.
