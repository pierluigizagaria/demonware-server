import hashlib
import json
from pathlib import Path
import sys
import tempfile
import unittest
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tools'))
from release_support import component, download, package_component, safe_name, version


class ReleaseTests(unittest.TestCase):
    def test_locked_package_rejects_wrong_revision_and_altered_payload(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'provider.zip'
            files = {'LICENSE': b'MIT', 'licenses/dependency.txt': b'notice', 'library.dll': b'binary'}
            package_component(path, files, 'orbisteamworks', 'v1.2.3', 'a' * 40, 'MIT', {})
            pin = dict(version='v1.2.3', revision='a' * 40, license='MIT')
            component(path, 'orbisteamworks', pin)
            with self.assertRaises(ValueError):
                component(path, 'orbisteamworks', dict(pin, revision='b' * 40))
            with zipfile.ZipFile(path) as original:
                contents = {name: original.read(name) for name in original.namelist()}
            contents['library.dll'] = b'altered'
            changed = Path(tmp) / 'changed.zip'
            with zipfile.ZipFile(changed, 'w') as archive:
                for name, data in contents.items():
                    archive.writestr(name, data)
            with self.assertRaises(ValueError):
                component(changed, 'orbisteamworks', pin)

    def test_cached_download_is_rechecked(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'input'
            path.write_bytes(b'bad')
            with self.assertRaises(ValueError):
                download({'url': 'https://example.invalid/input', 'sha256': hashlib.sha256(b'good').hexdigest()}, path)

    def test_archive_paths_and_release_versions(self):
        for value in ('../file', '/file', 'C:/file', 'a\\file', 'a/../file'):
            with self.assertRaises(ValueError):
                safe_name(value)
        for value in ('main', '--help', 'v1.2.3;echo', 'v1.2.3\n'):
            with self.assertRaises(ValueError):
                version(value)
        self.assertEqual(version('v1.2.3-preview.1'), 'v1.2.3-preview.1')

    def test_output_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'provider.zip'
            for attempt in range(2):
                files = {'LICENSE': b'MIT', 'licenses/dependency.txt': b'notice'}
                if attempt == 0:
                    package_component(path, files, 'orbisteamworks', 'v1.2.3', 'a' * 40, 'MIT', {})
                else:
                    with self.assertRaises(FileExistsError):
                        package_component(path, files, 'orbisteamworks', 'v1.2.3', 'a' * 40, 'MIT', {})


if __name__ == '__main__':
    unittest.main()
