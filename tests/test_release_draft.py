import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tools'))
import draft_release
from release_support import package_component


class DraftTests(unittest.TestCase):
    def invoke(self, directory, replies):
        with patch.object(sys, 'argv', ['draft_release.py', '--version', 'v1.2.3-preview.1',
                                      '--revision', 'a' * 40, '--dist', str(directory)]), \
             patch.dict(os.environ, {'GH_REPO': 'example/component'}), \
             patch.object(draft_release, 'output', side_effect=replies), \
             patch.object(draft_release, 'run') as run:
            draft_release.main()
            return run

    def test_existing_release_cannot_be_replaced(self):
        with self.assertRaisesRegex(ValueError, 'already exists'):
            self.invoke('unused', [json.dumps([[{'tag_name': 'v1.2.3-preview.1'}]])])

    def test_conflicting_tag_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'different source commit'):
            self.invoke('unused', ['[[]]', json.dumps([{'ref': 'refs/tags/v1.2.3-preview.1',
                                                       'object': {'type': 'commit', 'sha': 'b' * 40}}])])

    def test_verified_artifact_creates_draft_not_public_release(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            package_component(directory / 'component.zip', {'LICENSE': b'MIT', 'licenses/dep': b'notice'},
                              'orbisteamworks', 'v1.2.3-preview.1', 'a' * 40, 'MIT', {'source_dirty': False})
            run = self.invoke(directory, ['[[]]', '[]'])
            run.assert_called_once()
            self.assertIn('--draft', run.call_args.args)
            self.assertIn('--prerelease', run.call_args.args)
            self.assertIn('--target', run.call_args.args)
            self.assertNotIn('--clobber', run.call_args.args)

    def test_dirty_checkout_cannot_be_released(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            package_component(directory / 'component.zip', {'LICENSE': b'MIT', 'licenses/dep': b'notice'},
                              'orbisteamworks', 'v1.2.3-preview.1', 'a' * 40, 'MIT', {'source_dirty': True})
            with self.assertRaisesRegex(ValueError, 'clean-checkout'):
                self.invoke(directory, ['[[]]', '[]'])


if __name__ == '__main__':
    unittest.main()
