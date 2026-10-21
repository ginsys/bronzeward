"""Guard the scanner against actual serialization and Talos key-header forms."""

import unittest
import os
from pathlib import Path
import subprocess
import sys

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

    def test_optimization_refused_before_argument_parsing(self):
        for name in ("export.py", "run.py"):
            script = Path(__file__).with_name(name)
            for flags, optimization in ((["-O"], ""), (["-OO"], ""), ([], "1"), ([], "")):
                with self.subTest(script=name, flags=flags, optimization=optimization):
                    result = subprocess.run([sys.executable, *flags, str(script), "--help"],
                                            env=os.environ | {"PYTHONOPTIMIZE": optimization},
                                            capture_output=True, text=True)
                    if flags or optimization:
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn("requires assertions", result.stderr)
                        self.assertEqual(result.stdout, "")
                    else:
                        self.assertEqual(result.returncode, 0)
                        self.assertIn("usage:", result.stdout)
