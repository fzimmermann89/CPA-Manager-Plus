import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("shared_auth_sync", Path(__file__).with_name("sync.py"))
sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync)


class SharedAuthTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source, self.dest = self.root / "owner", self.root / "follower"
        self.source.mkdir()
        self.dest.mkdir()
        self.config = {"source": str(self.source), "destination": str(self.dest), "accounts": {"fz": "fz@example.test"}}
        self.owner = self.source / "owner.json"
        self.target = self.dest / "shared-codex-fz.json"
        self.value = {"type": "codex", "email": "fz@example.test", "access_token": "access-1", "refresh_token": "owner-secret", "id_token": "identity", "disabled": False,
                      "siwc": {"access_token": "siwc-1", "refresh_token": "siwc-secret", "client_id": "client"}}
        self.owner.write_text(json.dumps(self.value))

    def test_rotation_is_one_way_without_any_refresh_credentials(self):
        original = self.owner.read_bytes()
        self.assertEqual(sync.sync_once(self.config), 1)
        projected = json.loads(self.target.read_text())
        self.assertNotIn("refresh_token", self.target.read_text())
        self.assertEqual(projected["siwc"]["access_token"], "siwc-1")
        self.assertEqual(self.target.stat().st_mode & 0o777, 0o600)
        self.assertFalse(self.target.is_symlink())
        self.assertEqual(self.owner.read_bytes(), original)
        self.assertEqual(sync.sync_once(self.config), 0)
        projected.update(disabled=True, refreshToken="accidental-refresh")
        projected["nested"] = {"refresh_token": "another-secret"}
        self.target.write_text(json.dumps(projected))
        self.value.update(access_token="access-2", refresh_token="rotated-secret")
        self.owner.write_text(json.dumps(self.value))
        sync.sync_once(self.config)
        projected = json.loads(self.target.read_text())
        self.assertEqual(projected["access_token"], "access-2")
        self.assertTrue(projected["disabled"])
        self.assertNotIn("secret", self.target.read_text())
        self.assertNotIn("refreshToken", projected)

    def test_only_allowlisted_identity_and_remove_missing_or_ambiguous(self):
        (self.source / "other.json").write_text(json.dumps(dict(self.value, email="other@example.test")))
        sync.sync_once(self.config)
        self.assertEqual(len(list(self.dest.glob("*.json"))), 1)
        (self.source / "duplicate.json").write_text(json.dumps(self.value))
        sync.sync_once(self.config)
        self.assertFalse(self.target.exists())
        (self.source / "duplicate.json").unlink()
        sync.sync_once(self.config)
        self.owner.unlink()
        sync.sync_once(self.config)
        self.assertFalse(self.target.exists())

    def test_never_follow_destination_symlink(self):
        self.target.symlink_to(self.owner)
        original = self.owner.read_bytes()
        with self.assertRaises(ValueError):
            sync.sync_once(self.config)
        self.assertEqual(self.owner.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
