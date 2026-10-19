# Lane B: private Embedded Fresh CPA loopback isolation candidate

Baseline: product `v2@60f4ca76b0b78e7bbc6c2f8461dbc400afa74b37` (merged #1007), merged CPAMP Plan #47/#48.

**Status: NOT QUALIFIED.** This is an isolated, nonproduction network experiment. No Docker/live probe or CPA model-request authentication qualification was run for this change. Do not publish a Gateway or use this profile for actual traffic.

## Observed source-level risk

The current default Compose sends Ingress traffic to `cpamp-runtime:8317`, but both existing CPA seeds listen on `0.0.0.0:8317` (`host` in v7, `server.host` in the official v8 opt-in seed). The Supervisor's `CPAMP_RUNTIME_CPA_ADDR=127.0.0.1:8317` is a local readiness target, **not** the CPA bind configuration. No host `ports` mapping for 8317 does not prevent a peer on the Docker network from connecting directly to a running CPA process and bypassing Ingress.

## Bounded candidate

The standalone `docker-compose.fresh-auth-proof.yml` uses the same official pinned CPA v8.0.13 Linux/amd64 binary without modifying its source. A separate seed makes CPA listen on **127.0.0.1:8317**. Trusted Manager and Ingress both use `network_mode: service:cpamp-runtime` to share Runtime's network namespace. CPA (8317), Supervisor control (9081), Manager (18319) and Ingress control (9082) bind **loopback only**; Ingress alone binds the public listener inside that namespace. Only its port `${CPAMP_PROOF_PUBLIC_PORT:-28471}` is published on **host 127.0.0.1**, with the same internal and external port so Manager can verify the actual public origin.

Manager retains its original SQLite, protection key and Runtime token authority. Its loopback upstream and runtime URLs reach the shared namespace without exposing them to untrusted Docker network peers. The original Manager public-origin verification is preserved: both its configured URL and the Ingress socket resolve to `127.0.0.1:${CPAMP_PROOF_PUBLIC_PORT:-28471}` within the proof namespace. This is a topology candidate, **not** verification of Manager-to-CPA management/config writes or any Client Key authority. The normal Compose, default v7 runtime build, official v8 opt-in candidate, Legacy, External, Upgrade and release profiles are unchanged.

Current Fresh Ingress still returns **503** for model requests. Manager-staged Key records from #1007 do not prove effective Runtime keys. This network topology experiment intentionally does **not** create a Key verifier, runtime binding or an enabled Gateway.

## Runtime restart recovery — pending live requalification

Independent disposable Docker Desktop evidence for HEAD `3f53958a1b73e7867d663dfa42c9422718842326` revealed a real P2: a standalone `docker compose restart cpamp-runtime` moved Runtime to a new network namespace while Manager/Ingress retained the old namespace. All containers showed Up but the host Ingress endpoint returned an empty reply. Restarting the two consumers restored a shared namespace and Gateway Pending (503), but CPA was offline after its owning Runtime restarted. A stopped CPA is not a positive isolation proof.

This candidate adds **Compose-native `depends_on.restart: true`** for both shared-namespace consumers. It is intended to coordinate an *explicit* `docker compose restart cpamp-runtime` (Compose >= 2.17); **this fix has not yet been live-tested**. It does not cover `docker restart`, `docker compose restart --no-deps`, service restarts performed outside Compose, Docker daemon restarts, or container-runtime automatic restarts. The proof profile has `restart: 'no'` for each service; do not represent an unsupported restart mode as recovered.

For the independent recheck, after bringing up the same disposable project and starting CPA through the existing authorized Fresh Runtime lifecycle:

```sh
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml config
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml restart cpamp-runtime
```

Record the Compose event output, container restart timestamps, network namespace IDs for **all three** services, and public model request status. Confirm that Ingress and Manager actually rejoined Runtime's new namespace without separate manual restarts; otherwise this P2 is still **FAIL**. Runtime generation changes and the CPA child may be offline after Runtime restart by design: restore the **authorized** CPA lifecycle before retesting *live-process* ingress reachability and untrusted-peer isolation; do not bypass the Runtime gate or treat offline refusals as passing negative evidence. If the authorized re-start is unavailable, mark post-restart *live CPA isolation* **BLOCKED**.

Independently observe external non-local host access when the testing environment allows it. The present candidate's Docker bridge has IPv6 routing disabled; local `::1` checks do not qualify routed IPv6 exposure. Mark unsupported/out-of-profile routed IPv6 **NOT RUN** rather than creating a new network design solely for the experiment.

## Required real-process evidence — NOT RUN

Use only fresh disposable project volumes, synthetic secrets and a private linux/amd64 Docker host:

```sh
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml config
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml up -d --build
```

Run existing Fresh host initialization without importing any real database. Start CPA only through a separately authorized Runtime process path. Then independently record:

1. Exact source HEAD, Docker Compose resolved config, images and CPA executable digest; inspect all three services' shared network namespace. Verify that Manager can reach the *actual* public Ingress origin and that the Fresh routing verification path does not use an unreachable container-local port.
2. Confirm that **running CPA** is listening on `127.0.0.1:8317` only, including IPv6 behavior. From Ingress, connect to `127.0.0.1:8317` successfully. From a separate untrusted peer attached to the same Compose network, connection to `cpamp-runtime:8317` must fail. Check Manager (18319), Supervisor control (9081) and Ingress control (9082) as well: all must be unreachable from that peer. A refused connection while services are stopped is **not** isolation evidence.
3. Confirm host loopback's configured public port can reach Ingress; CPA `8317`, Manager `18319`, Supervisor `9081` and Ingress control `9082` must not be host-published. Enumerate additional supported host-network, sidecar, reverse-proxy and container-namespace bypasses instead of assuming Docker `expose` is a firewall.
4. Verify `/v1/models` and supported model/streaming entry points stay **503 without reaching CPA**, including across Ingress/Runtime restarts or reload. A clean network proof alone does not permit positive model traffic.
5. Record negative and positive Client Key cases (0 Key, missing/wrong/valid Key, last-Key change, authority outage, reload/restart) **only after** a CPAMP-owned per-request verifier tied to Manager's Canonical Key authority and independently proven Runtime-effective state exists. This candidate cannot satisfy those cases yet.

If a required observation is missing, mark it **UNKNOWN/STOP**, not PASS. Never use this configuration as an advertised safe Gateway. Stop the disposable project after collecting evidence; delete only this project's named volumes, not any existing user data.

## Gate and complexity

Gate: **P0/STOP** for public Gateway, public Beta, production exposure, External, Upgrade and release. No second identity store, general proxy framework, CPA fork or unrequested Runtime feature. Overdesign, overdefense, overabstraction and scope expansion: **PASS for this narrowly scoped candidate**. Independent network and process acceptance is still needed.
