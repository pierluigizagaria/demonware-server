"""Small, standard-library-only helpers owned by this repository's release tools."""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import urllib.parse
import urllib.request
import zipfile


def run(*args, **kwargs):
    subprocess.run([str(arg) for arg in args], check=True, **kwargs)


def output(*args):
    return subprocess.check_output([str(arg) for arg in args], text=True).strip()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def version(value):
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?', value):
        raise ValueError('Version must be vMAJOR.MINOR.PATCH with an optional prerelease suffix')
    return value


def revision(value):
    if not re.fullmatch(r'[0-9a-f]{40}', value):
        raise ValueError('A full source commit SHA is required')
    return value


def safe_name(name):
    path = PurePosixPath(name)
    if not name or path.is_absolute() or any(p in ('..', '.') for p in name.split('/')) or '\\' in name or ':' in name:
        raise ValueError(f'Unsafe archive member: {name}')
    return name


def download(spec, destination):
    destination = Path(destination)
    if urllib.parse.urlsplit(spec['url']).scheme != 'https':
        raise ValueError('Dependency downloads must use HTTPS')
    if not re.fullmatch(r'[0-9a-f]{64}', spec['sha256']):
        raise ValueError('Dependency requires a SHA-256 digest')
    if destination.exists():
        data = destination.read_bytes()
    else:
        with urllib.request.urlopen(spec['url'], timeout=120) as response:
            data = response.read()
    if digest(data) != spec['sha256']:
        raise ValueError(f'Checksum mismatch: {destination.name}')
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(data)
    return destination


def archive_member(archive_path, member):
    safe_name(member)
    with zipfile.ZipFile(archive_path) as archive:
        if archive.namelist().count(member) != 1:
            raise ValueError(f'Expected exactly one archive member: {member}')
        return archive.read(member)


def component(path, expected, pin=None):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)):
            raise ValueError('Duplicate archive entries')
        for name in names:
            safe_name(name)
        manifest = json.loads(archive.read('manifest.json'))
        if manifest['component'] != expected or manifest.get('platform') != 'windows-x64':
            raise ValueError('Unexpected component or platform')
        revision(manifest['revision'])
        version(manifest['version'])
        if pin:
            for key in ('version', 'revision', 'license'):
                if manifest[key] != pin[key]:
                    raise ValueError(f'{expected}: {key} differs from release-lock.json')
        files = {}
        for name, sha in manifest['files'].items():
            safe_name(name)
            data = archive.read(name)
            if digest(data) != sha:
                raise ValueError(f'{expected}: checksum mismatch for {name}')
            files[name] = data
        if set(names) - set(files) - {'manifest.json', 'SHA256SUMS.txt'}:
            raise ValueError('Unmanifested archive payload')
        if 'LICENSE' not in files or not any(name.startswith('licenses/') for name in files):
            raise ValueError('Required license notices are missing')
        return manifest, files


def write_zip(path, files):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(path, 'x', compression=zipfile.ZIP_DEFLATED) as archive:
        for name, data in sorted(files.items()):
            entry = zipfile.ZipInfo(safe_name(name), date_time=(1980, 1, 1, 0, 0, 0))
            entry.compress_type = zipfile.ZIP_DEFLATED
            entry.create_system = 3
            entry.external_attr = 0o100644 << 16
            archive.writestr(entry, data)
    path.with_suffix(path.suffix + '.sha256').write_text(f'{digest(path.read_bytes())}  {path.name}\n', encoding='ascii')


def package_component(path, files, name, tag, source_revision, license_name, build_info):
    manifest = dict(component=name, version=version(tag), revision=revision(source_revision),
                    platform='windows-x64', license=license_name, build=build_info,
                    files={name: digest(data) for name, data in sorted(files.items())})
    files['manifest.json'] = (json.dumps(manifest, indent=2) + '\n').encode()
    files['SHA256SUMS.txt'] = ''.join(f'{digest(data)}  {name}\n' for name, data in sorted(files.items())).encode()
    write_zip(path, files)
    component(path, name)


def build_info():
    return {'runner': os.environ.get('ImageOS', 'local'),
            'runner_image_version': os.environ.get('ImageVersion', 'local'),
            'source_dirty': bool(output('git', 'status', '--porcelain')),
            'workflow_commit': os.environ.get('GITHUB_SHA', output('git', 'rev-parse', 'HEAD')),
            'run_url': ('https://github.com/' + os.environ['GITHUB_REPOSITORY'] + '/actions/runs/' + os.environ['GITHUB_RUN_ID']) if 'GITHUB_RUN_ID' in os.environ else None}


def cmake_info(build):
    result = {'cmake': output('cmake', '--version').splitlines()[0]}
    for path in Path(build).glob('CMakeFiles/*/CMakeCXXCompiler.cmake'):
        text = path.read_text()
        for variable in ('CMAKE_CXX_COMPILER_ID', 'CMAKE_CXX_COMPILER_VERSION'):
            match = re.search(r'set\(' + variable + r' "([^"]+)"\)', text)
            if match:
                result[variable] = match.group(1)
    cache = (Path(build) / 'CMakeCache.txt').read_text()
    for variable in ('CMAKE_GENERATOR', 'CMAKE_GENERATOR_PLATFORM'):
        match = re.search(r'^' + variable + r':[^=]+=(.+)$', cache, re.MULTILINE)
        if match:
            result[variable] = match.group(1)
    return result
