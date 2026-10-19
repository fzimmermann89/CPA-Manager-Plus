# Lane B: private Embedded Fresh CPA loopback isolation candidate

Baseline: product `v2@60f4ca76b0b78e7bbc6c2f8461dbc400afa74b37` (merged #1007), merged CPAMP Plan #47/#48.

**Status: NOT QUALIFIED.** This is an isolated, nonproduction network experiment. No Docker/live probe or CPA model-request authentication qualification was run for this change. Do not publish a Gateway or use this profile for actual traffic.

## Observed source-level risk

The current default Compose sends Ingress traffic to `cpamp-runtime:8317`, but both existing CPA seeds listen on `0.0.0.0:8317` (`host` in v7, `server.host` in the official v8 opt-in seed). The Supervisor's `CPAMP_RUNTIME_CPA_ADDR=127.0.0.1:8317` is a local readiness target, **not** the CPA bind configuration. No host `ports` mapping for 8317 does not prevent a peer on the Docker network from connecting directly to a running CPA process and bypassing Ingress.

## Bounded candidate

The standalone `docker-compose.fresh-auth-proof.yml` uses the same official pinned CPA v8.0.13 Linux/amd64 binary without modifying its source. A separate seed makes CPA listen on **127.0.0.1:8317**, and Ingress uses `network_mode: service:cpamp-runtime` to reach that loopback address. Only Ingress port 18317 is published on **host 127.0.0.1**, owned by the Runtime container's network namespace.

Manager retains its original SQLite, protection key and Runtime token authority. Manager's existing upstream management URL may not reach the loopback-only CPA socket from its separate namespace; this PoC does not claim Manager-to-CPA management or configuration operations work. The normal Compose, default v7 runtime build, official v8 opt-in candidate, Legacy, External, Upgrade and release profiles are unchanged.

Current Fresh Ingress still returns **503** for model requests. Manager-staged Key records from #1007 do not prove effective Runtime keys. This network topology experiment intentionally does **not** create a Key verifier, runtime binding or an enabled Gateway.

## Required real-process evidence — NOT RUN

Use only fresh disposable project volumes, synthetic secrets and a private linux/amd64 Docker host:

```sh
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml config
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml up -d --build
```

Run existing Fresh host initialization without importing any real database. Start CPA only through a separately authorized Runtime process path. Then independently record:

1. Exact source HEAD, Docker Compose resolved config, images and CPA executable digest; inspect Ingress/Runtime shared network namespace and Manager's distinct namespace.
2. Confirm that **running CPA** is listening on `127.0.0.1:8317` only, including IPv6 behavior. From Ingress, connect to `127.0.0.1:8317` successfully. From a separate untrusted peer attached to the same Compose network, connection to `cpamp-runtime:8317` must fail. A refused connection while CPA is stopped is **not** isolation evidence.
3. Confirm `18317` can be reached only through host loopback and CPA `8317`, Supervisor `9081` and Ingress control `9082` are not host-published. Enumerate additional supported host-network, sidecar, reverse-proxy and container-namespace bypasses instead of assuming Docker `expose` is a firewall.
4. Verify `/v1/models` and supported model/streaming entry points stay **503 without reaching CPA**, including across Ingress/Runtime restarts or reload. A clean network proof alone does not permit positive model traffic.
5. Record negative and positive Client Key cases (0 Key, missing/wrong/valid Key, last-Key change, authority outage, reload/restart) **only after** a CPAMP-owned per-request verifier tied to Manager's Canonical Key authority and independently proven Runtime-effective state exists. This candidate cannot satisfy those cases yet.

If a required observation is missing, mark it **UNKNOWN/STOP**, not PASS. Never use this configuration as an advertised safe Gateway. Stop the disposable project after collecting evidence; delete only this project's named volumes, not any existing user data.

## Gate and complexity

Gate: **P0/STOP** for public Gateway, public Beta, production exposure, External, Upgrade and release. No second identity store, general proxy framework, CPA fork or unrequested Runtime feature. Overdesign, overdefense, overabstraction and scope expansion: **PASS for this narrowly scoped candidate**. Independent network and process acceptance is still needed.
