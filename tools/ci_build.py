"""Build, test and package the standalone Windows publisher."""
import argparse
import json
import os
from pathlib import Path
from release_support import build_info, output, package_component, run, version

ROOT = Path(__file__).resolve().parents[1]


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        text = text.lstrip()
        item, end = decoder.raw_decode(text)
        yield item
        text = text[end:]


def notices(files, directory, prefix):
    found = False
    for path in sorted(directory.rglob('*')):
        if path.is_file() and path.name.upper().startswith(('LICENSE', 'COPYING', 'NOTICE', 'PATENTS')):
            files[prefix + '/' + path.relative_to(directory).as_posix()] = path.read_bytes()
            found = True
    if not found:
        raise ValueError(f'No license notice found for {prefix}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', type=version)
    args = parser.parse_args()
    source_revision = output('git', 'rev-parse', 'HEAD')
    tag = args.version or 'v0.1.0-ci.' + source_revision[:12]
    os.environ['CGO_ENABLED'] = '0'
    os.environ['GOTOOLCHAIN'] = 'local'
    os.environ['GOFLAGS'] = '-mod=readonly'
    run('go', 'mod', 'download')
    run('go', 'mod', 'verify')
    run('go', 'vet', './...')
    run('go', 'test', '-count=1', '-timeout=180s', './...')
    run('go', 'build', '-trimpath', '-ldflags=-s -w', '-o', ROOT / 'bin/ci/demonware-server.exe', './cmd/ctrnf-server')
    files = {'demonware-server.exe': (ROOT / 'bin/ci/demonware-server.exe').read_bytes(),
             'LICENSE': (ROOT / 'LICENSE.md').read_bytes()}
    for filename in ('README.md', 'CREDITS.md', 'example.env'):
        files[filename] = (ROOT / filename).read_bytes()
    # Resolve the modules used by this command, including transitively linked modules.
    modules = {}
    for package in json_stream(output('go', 'list', '-deps', '-json', './cmd/ctrnf-server')):
        module = package.get('Module')
        if module and not module.get('Main'):
            if module.get('Replace'):
                raise ValueError('Release builds require canonical modules, not local replacements')
            modules[module['Path']] = module
    for name, module in sorted(modules.items()):
        notices(files, Path(module['Dir']), f'licenses/modules/{name}@{module["Version"]}')
    goroot = Path(output('go', 'env', 'GOROOT'))
    for name in ('LICENSE', 'PATENTS'):
        if (goroot / name).exists():
            files['licenses/go/' + name] = (goroot / name).read_bytes()
    # Go's standard library includes vendored code with its own notices.
    notices(files, goroot / 'src/vendor', 'licenses/go/vendor')
    files['licenses/go/vendor/modules.txt'] = (goroot / 'src/vendor/modules.txt').read_bytes()
    files['licenses/linked-modules.json'] = (json.dumps({name: module['Version'] for name, module in sorted(modules.items())}, indent=2) + '\n').encode()
    info = build_info()
    info['go'] = output('go', 'version')
    info['binary_modules'] = '\n'.join(output('go', 'version', '-m', ROOT / 'bin/ci/demonware-server.exe').splitlines()[1:])
    package_component(ROOT / 'dist' / f'demonware-server-{tag}-windows-x64.zip', files,
                      'demonware-server', tag, source_revision, 'PolyForm-Shield-1.0.0', info)


if __name__ == '__main__':
    main()
