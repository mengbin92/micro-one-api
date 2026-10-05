import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location('preflight', Path(__file__).with_name('rbac-cutover-preflight.py'))
PREFLIGHT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PREFLIGHT)


class PreflightTest(unittest.TestCase):
    def snapshot(self):
        return [dict(service=svc, state='running', image='sha256:verified',
                     dedicated_outbound=True, distinct_from_shared=True,
                     outbound_fingerprint=str(i), caller_map_valid=True,
                     correct_callers=list(PREFLIGHT.required_callers()[owner]),
                     incorrect_callers=[], sql_username=owner, configured_dsn_consistent=True, source_digest='a' * 64)
                for i, (svc, owner) in enumerate(PREFLIGHT.SERVICES.items())]

    def test_verified_instances(self):
        self.assertTrue(PREFLIGHT.assess(self.snapshot(), 'a' * 64)['ready'])

    def test_missing_caller_and_shared_db_block(self):
        rows = self.snapshot()
        rows[1]['correct_callers'] = []
        rows[1]['sql_username'] = 'root'
        report = PREFLIGHT.assess(rows, 'a' * 64)
        self.assertFalse(report['ready'])
        self.assertEqual(2, len(report['blockers']))

    def test_reused_service_credential_blocks(self):
        rows = self.snapshot()
        rows[1]['outbound_fingerprint'] = rows[0]['outbound_fingerprint']
        self.assertFalse(PREFLIGHT.assess(rows, 'a' * 64)['ready'])

    def test_config_channel_mismatch_blocks(self):
        rows = self.snapshot()
        rows[1]['configured_dsn_consistent'] = False
        self.assertFalse(PREFLIGHT.assess(rows, 'a' * 64)['ready'])


if __name__ == '__main__':
    unittest.main()
