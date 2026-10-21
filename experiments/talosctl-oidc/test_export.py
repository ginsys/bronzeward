"""Guard the scanner against actual serialization and Talos key-header forms."""

import unittest

from export import encoded, leaks


class LeakScanTest(unittest.TestCase):
    def test_ed25519_without_known_value(self):
        self.assertTrue(leaks(encoded({"unexpected": "-----BEGIN ED25519 PRIVATE KEY-----\nsynthetic"}), []))

    def test_json_escaped_known_value(self):
        value = b'synthetic-secret-with-"quotes"\nand-backslash\\'
        data = encoded({"unexpected": value.decode()})
        self.assertNotIn(value, data)
        self.assertTrue(leaks(data, [value]))

    def test_safe_metadata(self):
        self.assertFalse(leaks(encoded({"type": "REFRESH_TOKEN_ERROR", "reason": "User disabled"}),
                               [b"synthetic-known-secret"]))
