#!/usr/bin/env python3
"""Destructive, private-only CPA v8.0.13 Client Key effect experiment.

Run ONLY inside the trusted namespace of a fresh, disposable #1008 proof
profile, after verifying the official CPA binary and empty synthetic volumes.
This tool never opens the CPAMP Gateway and never declares runtimeEffective.
"""

import argparse
import http.client
import json
import os
import secrets
import sys
import time


class StopProof(Exception):
    pass


def request(port, method, path, authorization=None, payload=None):
    headers = {"Accept": "application/json", "Cache-Control": "no-store"}
    if authorization:
        headers["Authorization"] = "Bearer " + authorization
    body = None
    if payload is not None:
        body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        headers["Content-Type"] = "application/json"
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
    try:
        connection.request(method, path, body=body, headers=headers)
        response = connection.getresponse()
        data = response.read(1 << 20)
        if response.read(1):
            raise StopProof("HTTP response exceeded evidence limit")
        return response.status, data
    except OSError as exc:
        raise StopProof("trusted loopback service unreachable") from exc
    finally:
        connection.close()


def json_value(port, path, management_key):
    status, data = request(port, "GET", path, management_key)
    if status != 200:
        raise StopProof("Management safe read rejected: HTTP " + str(status))
    try:
        return json.loads(data)
    except (ValueError, UnicodeError) as exc:
        raise StopProof("Management returned invalid JSON") from exc


def check_ingress(port, phase, evidence):
    for mode in ("absent", "wrong", "valid"):
        # The public Gateway MUST stay Pending irrespective of the client key.
        status, _ = request(port, "GET", "/v1/models",
                            {"absent": None, "wrong": "synthetic-invalid",
                             "valid": evidence["_latest_secret"]}[mode])
        evidence["events"].append({"phase": phase, "edge": "ingress",
                                   "credential": mode, "status": status})
        if status != 503:
            raise StopProof("Ingress forwarded or changed Pending at " + phase)


def check_cpa(port, phase, tokens, evidence):
    statuses = {}
    for name, token in tokens.items():
        status, _ = request(port, "GET", "/v1/models", token)
        evidence["events"].append({"phase": phase, "edge": "private-cpa",
                                   "credential": name, "status": status})
        statuses[name] = status
    return statuses


def set_only_client_keys(port, management_key, values, phase, evidence):
    # No retries: a timeout leaves write state unknown and the experiment STOPS.
    status, _ = request(port, "PUT", "/v8/management/config/access/api-keys",
                        management_key, values)
    evidence["events"].append({"phase": phase, "action": "config-put",
                               "status": status, "keyCount": len(values)})
    if status != 200:
        raise StopProof("Official CPA Client Key write failed at " + phase)
    stored = json_value(port, "/v8/management/config/access/api-keys",
                        management_key)
    if stored != values:
        raise StopProof("Management configuration readback mismatch at " + phase)
    # Readback is not an effective-provider acknowledgment.


def await_auth(port, phase, expected, tokens, ingress_port, evidence, deadline):
    end = time.monotonic() + deadline
    while True:
        check_ingress(ingress_port, phase, evidence)
        observed = check_cpa(port, phase, tokens, evidence)
        if observed == expected:
            return
        if time.monotonic() >= end:
            raise StopProof("Live CPA auth did not reach expected state at " + phase)
        time.sleep(0.25)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cpa-port", type=int, default=8317)
    parser.add_argument("--ingress-port", type=int, default=28471)
    parser.add_argument("--reload-seconds", type=float, default=15)
    args = parser.parse_args()
    if (os.environ.get("CPAMP_PRIVATE_CPA_KEY_PROOF") != "1" or
            os.environ.get("CPAMP_PROOF_DISPOSABLE_VOLUME") != "1"):
        raise StopProof("Disposable proof must be explicitly attested in environment")
    if (args.cpa_port != 8317 or not 1 <= args.ingress_port <= 65535 or
            args.ingress_port == args.cpa_port or not 1 <= args.reload_seconds <= 60):
        raise StopProof("Unsupported proof ports or reload budget")
    management = os.environ.get("CPAMP_PROOF_CPA_MANAGEMENT_KEY", "")
    if len(management) < 32 or any(ch.isspace() for ch in management):
        raise StopProof("Missing private synthetic Management credential")

    evidence = {"result": "STOP", "binary": "official-v8.0.13-verify-out-of-band",
                "events": [], "_latest_secret": None}
    cfg = json_value(args.cpa_port, "/v8/management/config", management)
    if not isinstance(cfg, dict):
        raise StopProof("Management config projection is not an object")
    server = cfg.get("server", {})
    management_cfg = cfg.get("management", {})
    plugins = cfg.get("plugins", {})
    if (cfg.get("config-version") != 8 or
            not isinstance(server, dict) or server.get("host") != "127.0.0.1" or
            server.get("port") != 8317 or not isinstance(management_cfg, dict) or
            management_cfg.get("allow-remote") is not False or
            not isinstance(plugins, dict) or plugins.get("enabled") is not False):
        raise StopProof("Management view is not the constrained loopback CPA seed")
    if json_value(args.cpa_port, "/v8/management/config/access/api-keys",
                  management) != []:
        raise StopProof("Refusing to touch nonempty Client Key configuration")

    first = "cpamp_sk_" + secrets.token_hex(32)
    second = "cpamp_sk_" + secrets.token_hex(32)
    evidence["_latest_secret"] = first
    try:
        check_ingress(args.ingress_port, "initial", evidence)
        # With CPA's EMPTY provider list, no client authorization occurs.
        baseline = check_cpa(args.cpa_port, "empty", {"absent": None}, evidence)
        if baseline["absent"] != 200:
            raise StopProof("CPA zero-provider baseline differs from pinned contract")

        set_only_client_keys(args.cpa_port, management, [first], "install", evidence)
        await_auth(args.cpa_port, "install",
                   {"absent": 401, "wrong": 401, "valid": 200},
                   {"absent": None, "wrong": "synthetic-invalid", "valid": first},
                   args.ingress_port, evidence, args.reload_seconds)

        set_only_client_keys(args.cpa_port, management, [second], "replace", evidence)
        evidence["_latest_secret"] = second
        await_auth(args.cpa_port, "replace",
                   {"old": 401, "new": 200, "absent": 401},
                   {"old": first, "new": second, "absent": None},
                   args.ingress_port, evidence, args.reload_seconds)

        set_only_client_keys(args.cpa_port, management, [], "clear-last", evidence)
        # Raw CPA may revert to *anonymous* access. This is an observed hazard,
        # never a CPAMP grant. Ingress must still be 503 for every attempt.
        await_auth(args.cpa_port, "clear-last", {"absent": 200},
                   {"absent": None}, args.ingress_port, evidence,
                   args.reload_seconds)
        evidence["result"] = "PASS_WITH_EXPLICIT_STOPS"
    finally:
        # No automated rollback/retry after an uncertain mutation. Destroy the
        # entire proof stack and synthetic volumes under operator control.
        evidence.pop("_latest_secret", None)
        print(json.dumps(evidence, sort_keys=True), flush=True)


if __name__ == "__main__":
    try:
        main()
    except StopProof as exc:
        print(json.dumps({"result": "STOP", "reason": str(exc),
                          "gatewayAuthorized": False,
                          "runtimeEffective": False}), file=sys.stderr)
        sys.exit(1)
