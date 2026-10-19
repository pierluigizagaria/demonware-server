"""Create a new draft from verified artifacts; never replace an existing release."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import zipfile
from release_support import digest, output, revision, run, version


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True, type=version)
    parser.add_argument('--revision', required=True, type=revision)
    parser.add_argument('--dist', type=Path, default=Path('dist'))
    args = parser.parse_args()
    repo = os.environ['GH_REPO']
    # Fail closed on API errors; the list includes drafts visible to this token.
    releases = json.loads(output('gh', 'api', '--paginate', '--slurp', f'repos/{repo}/releases?per_page=100'))
    if any(item['tag_name'] == args.version for page in releases for item in page):
        raise ValueError('This release already exists. Choose a new version; existing assets are never replaced.')
    refs = json.loads(output('gh', 'api', f'repos/{repo}/git/matching-refs/tags/{args.version}'))
    for ref in refs:
        if ref['ref'] == 'refs/tags/' + args.version:
            obj = ref['object']
            while obj['type'] == 'tag':
                obj = json.loads(output('gh', 'api', f'repos/{repo}/git/tags/{obj["sha"]}'))['object']
            if obj['sha'] != args.revision:
                raise ValueError('Existing tag points at a different source commit')
    archives = sorted(args.dist.glob('*.zip'))
    if len(archives) != 1:
        raise ValueError('Expected exactly one release ZIP')
    archive = archives[0]
    checksum = archive.with_suffix('.zip.sha256')
    if checksum.read_text().strip() != f'{digest(archive.read_bytes())}  {archive.name}':
        raise ValueError('Release ZIP checksum mismatch')
    with zipfile.ZipFile(archive) as package:
        manifest_name = 'manifest.json' if 'manifest.json' in package.namelist() else 'ctrnf-p2p-manifest.json'
        manifest = json.loads(package.read(manifest_name))
        source_revision = manifest.get('revision') or manifest['components']['ctrnf-p2p']['revision']
        if manifest['version'] != args.version or source_revision != args.revision:
            raise ValueError('Release version/revision differs from built artifact')
        if manifest.get('build', {}).get('source_dirty') is not False:
            raise ValueError('Only clean-checkout builds may become draft releases')
    notes = args.dist / 'release-notes.md'
    notes.write_text(f'Windows x64 preview built from `{args.revision}`.\n\n'
                     'The attached ZIP includes source revisions, SHA-256 hashes and license notices.\n\n'
                     'Automated build, offline tests and package checks passed. Live Steam and multi-PC gameplay require separate validation before publication.\n', encoding='utf-8')
    run('gh', 'release', 'create', args.version, archive, checksum, '--repo', repo,
        '--target', args.revision, '--draft', '--prerelease', '--latest=false',
        '--title', f'{manifest["component"]} {args.version}', '--notes-file', notes)


if __name__ == '__main__':
    main()
