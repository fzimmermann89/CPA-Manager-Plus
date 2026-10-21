#!/usr/bin/env python3
"""One-way access-credential projection; compatible with Uberspace's Python 3.6.

The destination never receives an OAuth refresh token or a link to owner files.
Atomic replacements notify CLIProxyAPI's existing auth-directory watcher.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import signal
import tempfile
import threading

CREDENTIAL_FIELDS = (
    "type", "access_token", "id_token", "account_id", "email", "expired",
    "last_refresh", "plan_type", "use_siwc",
)
SIWC_FIELDS = ("access_token", "access", "expired", "expires_at", "client_id", "device_id")
FORBIDDEN = {"refresh_token", "refreshToken", "refresh", "refresh-token"}


def strip_refresh(value):
    if isinstance(value, dict):
        return {k: strip_refresh(v) for k, v in value.items() if k not in FORBIDDEN}
    if isinstance(value, list):
        return [strip_refresh(v) for v in value]
    return value


def read_object(path):
    if path.is_symlink():
        raise ValueError("symlinks are not accepted")
    with path.open() as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError("expected an object")
    return value


def atomic_write(path, value):
    fd, temporary = tempfile.mkstemp(prefix=".shared-auth-", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w") as handle:
            json.dump(value, handle, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, str(path))
        os.chmod(str(path), 0o600)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def sync_once(config):
    source = Path(config["source"])
    destination = Path(config["destination"])
    if source.resolve() == destination.resolve():
        raise ValueError("owner and follower directories must differ")
    if not source.is_dir() or not destination.is_dir():
        raise ValueError("auth directory missing")
    allowed = config["accounts"]
    if not allowed or any(not label.isalnum() for label in allowed):
        raise ValueError("invalid account labels")
    if len(set(email.lower() for email in allowed.values())) != len(allowed):
        raise ValueError("duplicate identity")
    matches = {email.lower(): [] for email in allowed.values()}
    for path in source.glob("*.json"):
        try:
            value = read_object(path)
        except (OSError, ValueError):
            continue
        email = str(value.get("email", "")).lower()
        if email in matches and value.get("type") == "codex":
            matches[email].append(value)
    changed = 0
    for label, email in sorted(allowed.items()):
        target = destination / ("shared-codex-" + label + ".json")
        if target.is_symlink():
            raise ValueError("follower must not link to owner files")
        candidates = matches[email.lower()]
        # Missing, ambiguous or unusable owner credentials fail closed.
        if len(candidates) != 1 or not candidates[0].get("access_token"):
            if target.exists():
                target.unlink()
                changed += 1
            continue
        owner = candidates[0]
        try:
            current = read_object(target)
        except FileNotFoundError:
            current = {}
        # Retain independent follower settings (disabled, priority, etc.), but
        # never retain a token that the follower might refresh on its own.
        projected = strip_refresh(current)
        for key in CREDENTIAL_FIELDS + ("siwc",):
            projected.pop(key, None)
        projected.update({key: owner[key] for key in CREDENTIAL_FIELDS if key in owner})
        if isinstance(owner.get("siwc"), dict):
            projected["siwc"] = {key: owner["siwc"][key] for key in SIWC_FIELDS if key in owner["siwc"]}
        projected.setdefault("disabled", False)
        projected["shared_auth_owner"] = "privat"
        if projected != current:
            atomic_write(target, projected)
            changed += 1
    return changed


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("config")
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    os.umask(0o077)
    config_path = Path(args.config)
    config = read_object(config_path)
    # A process-lifetime lock prevents accidentally running two writers.
    with config_path.with_suffix(".lock").open("a") as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        stop = threading.Event()
        signal.signal(signal.SIGTERM, lambda *_: stop.set())
        signal.signal(signal.SIGINT, lambda *_: stop.set())
        failed = False
        while not stop.is_set():
            try:
                changed = sync_once(config)
                if changed or failed:
                    print("Shared access credentials synchronized ({0} changes).".format(changed), flush=True)
                failed = False
            except (OSError, ValueError) as error:
                if not failed:
                    # Do not log file contents, token values, or exception text.
                    print("Credential synchronization failed: " + type(error).__name__, flush=True)
                failed = True
                if args.once:
                    raise SystemExit(1)
            if args.once:
                break
            stop.wait(1)


if __name__ == "__main__":
    main()
