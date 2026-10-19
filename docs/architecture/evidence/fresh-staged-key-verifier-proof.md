# Private Fresh staged-key verifier proof — NOT Runtime authorization

This narrowly scoped opt-in diagnostic is an intermediate Lane B consumer of the existing Manager-owned canonical Fresh Client API Key records from product #1007. It **does not qualify, configure or activate a Gateway**, and never declares a key effective in CPA.

## Default behavior / access boundary

- The exact `POST /internal/fresh-client-key-stage/probe` route is absent unless the **Manager** process explicitly sets `CPAMP_PRIVATE_CLIENT_AUTH_PROOF=1`. The variable is not set in the default Compose or the separate #1008 experiment.
- The Manager handler checks the actual TCP peer remote address for IPv4/IPv6 loopback, requires the exact unescaped, queryless path, and does not accept browser Admin credentials as Client Keys. The public Ingress classifier always denies the corresponding private namespace locally, including encoded variants, so credentials cannot fall through to CPA in Legacy or Fresh mode.
- `Authorization: Bearer <fresh-client-key>` is checked directly against the current Manager-owned staged hashes and protected records. Missing/invalid -> 401; database or protection authority failure -> 503; wrong method -> 405; disabled, nonloopback or noncanonical path -> 404.
- A matching credential returns `200 {"managerStagedKeyMatched":true,"runtimeEffective":false,"gatewayAuthorized":false}`. This is **diagnostic** and cannot authorize a model request. No other public authority, key store or CPA provider configuration is created.
- This is not a second Key authority; no secrets are returned, logged or copied into Ingress storage.

## STOP and prerequisites for a real Ingress consumer

An eventual private Ingress gate MUST separately prove canonical Key identity/state and exact CPA Runtime-effective authorization, including zero Key, bad/missing Key, last Key removal, timeout/reload/restart, and no raw-CPA path bypass. A successful staged-key proof response alone MUST never unlock forwarding. Until that qualified consumer exists, Fresh Gateway remains 503 Pending.

Cross-generation CPA reactivation and post-restart live isolation from #1008 remain BLOCKED. This PR does not loosen the Fresh Strategy identity/generation fence, modify Compose, or attempt Runtime recovery.

## Test/rollback

Unit/integration regressions cover zero Key, correct/wrong Key, `missing`/`superseded` canonical Key state, corrupted DB or encryption key, disabled endpoint, method, encoded path and Ingress deny. The private Manager handler is also tested in a separate child process over a real IPv4 loopback TCP socket (enabled/disabled and positive/negative requests); a public Ingress TCP listener verifies that neither Fresh nor Legacy forwards the private path to Manager or CPA. Nonloopback Manager peer rejection is tested with synthetic request peers only. The original #1010 test suite does not itself claim an external-observer, Docker-network, live CPA, or Runtime-effective proof. The later independent #1008 + #1010 joint run on v2@5de8b272223d59b78f56e899c9fe4aa161e2f37f did prove the limited opt-in Docker IPv4 topology, Manager probe, and Pending non-forwarding. To roll back the opt-in diagnostic, leave the variable unset (default). The source experiment is merged, but Gateway authorization remains STOP.

Overdesign, overdefense, overabstraction and scope expansion: PASS — no new persistence, authentication platform, model proxy, CPA fork or release behavior.


## Pinned CPA v8.0.13 Runtime-effective Client Key contract audit (read-only, 2026-10-19)

**Decision: NO-GO for model forwarding.** The Manager's canonical staged keys and the CPA process's actual request-authentication providers are distinct authorities. A positive private probe (`managerStagedKeyMatched=true`) is not an effective-runtime or Gateway grant.

### Confirmed contracts from the exact upstream tag

1. **Persistent config is not active provider state.** [CPA v8.0.13 ConfigV8](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/api/handlers/management/config_v8.go) serves `GET /v8/management/config/access/api-keys` from the saved YAML tree. A successful write returns `200` after `WriteConfig`, but calls `reloadConfigAfterManagementSaveAsync` before returning. [Its reload handler](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/api/handlers/management/handler.go) launches the application callback in another goroutine. Neither write success nor subsequent disk readback is an acknowledgment that the request-authentication manager has applied the keys.
2. **Effective auth resides behind a separate live provider manager.** [CPA access reconciliation](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/access/reconcile.go) can replace its provider list after reload. [The config key provider](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/access/config_access/provider.go) unregisters itself when no configured keys remain. [CPA's access manager](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/sdk/access/manager.go) returns a nil result/error when it has no providers; [the HTTP middleware](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/api/server_middleware.go) then continues the request. This is the confirmed zero-key bypass risk; it must not be used as CPAMP security behavior.
3. **No sufficient effective-key attestation was identified in the reviewed v8.0.13 management contract.** The documented config endpoint observes a persisted view, not the current request-auth provider set or an applied revision bound to the running process and Runtime generation. A later process restart can change this state again. This is a bounded conclusion about the inspected interfaces, not a claim that every internal SDK path was exhaustively surveyed.
4. **Present Embedded Fresh has no Key application owner.** The opt-in [loopback CPA seed](../../../docker/runtime/config.v8.loopback-proof.yaml) starts with `access.api-keys: []` and an empty `management.secret-key` (Management API unavailable without a separately configured password). [Fresh Manager Key CRUD](../../../apps/manager-server/cmd/cpa-manager-plus/fresh_keys.go) only creates/lists/looks up encrypted staged records, reports `runtimeState=not_applied` and `revocationAvailable=false`, and intentionally has no delete/replace or CPA config writer. [Existing read-only v8 adapter](../../../apps/manager-server/internal/service/cpa/v8.go) returns `ClientKeyCount` from a management config view and expressly does not qualify Runtime or Gateway. The private probe never writes CPA.

### Minimum exit criteria before proposing any enabled model consumer

- **Explicit owner and authorization first:** identify the one approved CPAMP-owned consumer for staging-to-official-CPA configuration application and its management credential/lifecycle protection. Existing B1 CPA config-write STOP remains in force. Do not use the private staged-key probe, create a second durable Key authority, or silently enable CPA Management API in the experimental seed.
- **Live effective evidence:** in one disposable, generation-bound, trusted CPA process with a synthetic upstream, demonstrate actual request-auth decisions **at the CPA data-plane** after any proposed key application, not just `PUT 200`, `GET` equality or a nonempty list. Observe absent/wrong/valid keys and prove which requests reach the upstream. Where active provider state cannot be demonstrated reliably, leave Ingress Pending.
- **Revocation and state changes:** do not claim effective authorization on a partial CRUD surface. Independently prove last-key denial, key replacement/revocation, async reload and stale-provider races, Manager/Runtime authority loss, process restart and cross-generation fencing. The current Fresh API does not yet expose an approved revoke/apply contract; record these as **NOT IMPLEMENTED / NOT RUN**, not implicit success.
- **No bypass at the actual boundary:** retain the bounded #1008 loopback namespace model; revalidate live CPA reachability for any new deployment/restart profile, and inspect *all* supported model/streaming/upgrade ingress routes before admitting requests. Preserve `409 managed_target_identity_mismatch`; post-restart live CPA remains **BLOCKED**.
- **Fail closed until complete:** Manager-staged match, CPA config readback, ConfigV8 write success or live child health can be supporting diagnostics, but none is sufficient by itself to set `runtimeEffective=true` or `gatewayAuthorized=true`. Public Gateway/Beta/External/Upgrade/Release remain **STOP**.

This is a source-only feasibility finding. No new Management write, runtime process experiment, release qualification, client-Key revocation implementation, Gateway admission rule or recovery path was performed or authorized by this audit. Overdesign, overdefense, overabstraction and scope expansion: PASS — reuse the current authority and private topology; do not add an attestation framework without a qualifying consumer.


## Official CPA v8.0.13 private key-effect test (Plan #52)

The test-only [runner](../../../bin/ci/fresh-v8-private-key-effective-probe.py) makes **destructive client-key configuration writes**. It must run exclusively in a fresh, disposable, private #1008 Compose profile, never against a user's running CPA, existing volumes or actual credentials. The merged [Plan #52](https://github.com/seakee/cpamp-plan/pull/52) authorizes only this bounded experiment, **not** a Manager configuration writer or Gateway enablement.

**Preflight and invocation:**

1. Create a unique disposable Compose project with empty synthetic volumes. Use only the existing pinned official v8.0.13 loopback-proof image; independently verify the live CPA executable SHA-256 `b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41` and record the exact source, image and runtime generation.
2. Before *first* Runtime start, make an untracked, private, temporary copy of `docker/runtime/config.v8.loopback-proof.yaml` with a fresh strong synthetic `management.secret-key`. Mount it read-only at `/usr/local/share/cpamp/runtime/config.seed.yaml` in an isolated, untracked Compose override. The entrypoint copies that seed only if `/runtime/gateway/config.yaml` is absent. Do not change tracked seeds or start with any existing volume. Keep Management, CPA and Runtime control ports loopback-only.
3. Complete authorized Fresh bootstrap/start using the existing operation and generation contracts. Confirm live CPA, no user/provider/OAuth credentials or real upstream configuration, and initially empty `access.api-keys`.
4. Use the one-shot Python container below to join the *trusted Runtime network namespace*. Runtime has neither Python nor this script; do not install either in the product image. Only `127.0.0.1:8317` and `127.0.0.1:<ingress-port>` are addressed. The opt-in flags **do not** replace explicit verification of new empty volumes.
5. The runner checks private CPA model auth before and after official `PUT /v8/management/config/access/api-keys` install/replace/clear operations and demands public Ingress `503` throughout. Each phase needs **at least 2 seconds and 5 consecutive matching sweeps**, within the total `--reload-seconds` budget (default 15; allowed 3–60). Old-to-new transient decisions are recorded; an unexpected HTTP status, per-credential regression after first reaching its target, unreachable service or timeout stops immediately. Each HTTP result and sweep has UTC and monotonic elapsed timestamps. Sequential sweeps are sampled evidence, **not an atomic observation or a reload acknowledgment**. Reports never contain plaintext keys. If the last CPA Key removal restores anonymous access, record this as official CPA behavior, not CPAMP authorization.
6. A separate independent observer must still verify whether real synthetic model requests reach a synthetic upstream; the script's `/v1/models` request decisions do **not** prove upstream nonarrival/arrival, streaming or all supported model routes. On any unconfirmed write, unexpected response, topology change, security discrepancy or preserved `409` fence, STOP and destroy only the new disposable experiment containers/network/volumes. Do not retry uncertain writes, leak raw synthetic secrets into public issues, or bypass the generation fence.

This experiment does **not** implement automatic CPA config application, effective-Key attestation, revocation, reload completion acknowledgment, Gateway admission, or cross-generation restart recovery. Only the previously demonstrated Pending Ingress behavior is allowed. Gateway/Beta/External/Upgrade/Release continue STOP.

Overdesign, overdefense, overabstraction and scope expansion: PASS; one test-only runner, focused offline regressions and this existing evidence document, no new product owner or storage authority.

### Reproducible one-shot Python execution

Run from the exact reviewed worktree. Docker Engine must be Linux/amd64; Compose must support the existing namespace-sharing proof profile. Prepare a **unique** project and a private ignored scratch directory; check the intended public loopback port is free. Do not enable shell tracing. Never print the seed, environment file, full container environment, Management config body, initialization handoff or bootstrap responses.

```sh
umask 077
mkdir -p bin/tmp
proof_dir=$(mktemp -d "$PWD/bin/tmp/pr1012-private.XXXXXX")
export proof_dir
python3 - <<'PY'
import json, os, pathlib, secrets
p = pathlib.Path(os.environ['proof_dir'])
key = 'cpamp_proof_management_' + secrets.token_hex(32)
seed = pathlib.Path('docker/runtime/config.v8.loopback-proof.yaml').read_text()
assert seed.count("secret-key: ''") == 1
(p / 'seed.yaml').write_text(seed.replace("secret-key: ''", 'secret-key: ' + json.dumps(key)))
(p / 'probe.env').write_text('CPAMP_PRIVATE_CPA_KEY_PROOF=1\n'
    'CPAMP_PROOF_DISPOSABLE_VOLUME=1\nCPAMP_PROOF_CPA_MANAGEMENT_KEY=' + key + '\n')
PY
```

Use an **untracked** override to bind `$proof_dir/seed.yaml` read-only at `/usr/local/share/cpamp/runtime/config.seed.yaml` on `cpamp-runtime`, before its first start. Keep the existing `docker-compose.fresh-auth-proof.yml` network topology, volume declarations and port bindings; no Docker socket, extra published ports, external volumes or user paths. Build reviewed source with task-specific image tags. Complete the existing Fresh initialize/claim/Admin/new-managed strategy workflow. The existing 409 generation fence is a STOP, never an invitation to reset the database or call a different Start path.

Before any Client Key write, verify and privately record:

- Project/container/network/volume creation times and Compose labels; names must not have existed before this run. Inspect the resolved Compose config privately and assert every mount belongs to this new project except the single synthetic seed. Confirm initially empty volume contents **before starting services**, and no OAuth/auth/plugin files before running the probe.
- Container IDs, image IDs, Linux/amd64, actual process executable/arguments/UID, `/proc/<cpa-pid>/exe` SHA-256, current Runtime identity/generation/active artifact and network namespace IDs. Recheck the same identity after execution; no service restart or other writer may overlap the run. Do not use stale image tags or a saved checksum alone.
- CPA 8317, Runtime 9081, Manager 18319 and Ingress control 9082 listen only on loopback. Only Ingress is published at host `127.0.0.1:$CPAMP_PROOF_PUBLIC_PORT`. An ordinary same-bridge participant must fail to connect to private ports, with a reachable Ingress/503 positive control.
- Full persisted seed/config contains only the constrained synthetic settings; no Provider, OAuth, existing Client Key, environment credentials, plugin or real upstream. Management credential comes from the private seed above. Runtime image remains without Python (`docker exec "$runtime_id" sh -c '! command -v python3'`). Read-only Management config/key-list checks must agree before writes.

The runner accepts only this seed's config shape, including disabled panel updates/log persistence/usage collection. Additional roots (including historical provider aliases or Amp upstream settings), OAuth options, TLS/bind changes and plugin settings are rejected before writes. It rechecks the previous Client Key list immediately before each of its three writes and stops on ownership drift. This is not a compare-and-swap API: the verified disposable topology **must have no concurrent writer**. The runner does not inspect Docker or `/proc`; those identity/storage checks remain explicit operator prerequisites, not self-attestation through opt-in environment flags. Synthetic upstream execution is **NOT RUN by this runner**; adding a Provider is outside its fixed empty-seed contract.

After these checks, set `runtime_id` from the **exact** project's `cpamp-runtime` container, `probe_image` to a locally inspected Linux/amd64 Python image ID (for example `python:3.13-alpine` resolved with `docker image inspect --format '{{.Id}}'`), and `CPAMP_PROOF_PUBLIC_PORT` to the profile's actual port. Then:

```sh
test -n "$runtime_id" && test -n "$probe_image"
docker run --rm --pull=never --platform linux/amd64 \
  --network "container:$runtime_id" --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 65534:65534 \
  --env-file "$proof_dir/probe.env" \
  --mount "type=bind,src=$PWD/bin/ci/fresh-v8-private-key-effective-probe.py,dst=/probe.py,readonly" \
  "$probe_image" python3 -B /probe.py --ingress-port "$CPAMP_PROOF_PUBLIC_PORT" \
  >"$proof_dir/probe.json" 2>"$proof_dir/probe.stderr"
```

No production image change, extra port or Docker socket is required. Read the exit status and both evidence files even on failure; STOP means no further config write, retry or automatic rollback. Keep the private evidence directory mode 0700/files 0600. Only publish a reviewed, secret-scanned summary. Destroy only the task-owned project containers/network/new volumes after collecting evidence; never run a global prune. Protect/remove private seed material under the same task-owned cleanup scope.

Offline checks (safe without Docker or credentials):

```sh
python3 -m py_compile bin/ci/fresh-v8-private-key-effective-probe.py bin/ci/fresh-v8-private-key-effective-probe-test.py
python3 bin/ci/fresh-v8-private-key-effective-probe-test.py
```
