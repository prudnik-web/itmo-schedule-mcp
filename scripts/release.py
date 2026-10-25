#!/usr/bin/env python3
"""Build allowlisted release archives locally; does not upload or publish."""
import argparse
import hashlib
import json
import os
import pathlib
import re
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
TARGETS = ('darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64',
           'windows/amd64', 'windows/arm64')
DOCUMENTS = ('README.md', 'LICENSE', 'SECURITY.md', 'docs/calendar-sync.md',
             'docs/google-setup.md', 'docs/troubleshooting.md', 'docs/references.md',
             'docs/deployment.md', 'docs/agent-integration.md',
             'docs/input.schema.json', 'docs/output.schema.json', 'docs/schedule.example.json',
             'examples/codex.example.toml', 'examples/mcp.example.json',
             'deploy/itmo-schedule-mcp.service', 'deploy/Caddyfile.routes',
             'deploy/Caddyfile.logging', 'deploy/server.env.example',
             'CONTRIBUTING.md')


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        item, end = decoder.raw_decode(text.lstrip())
        yield item
        text = text.lstrip()[end:]


def dependency_notices(targets):
    """Retain notices for the runtime and modules used by distributed commands."""
    modules = {}
    for target in targets:
        goos, goarch = target.split('/')
        env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0', GOWORK='off')
        result = subprocess.run(['go', 'list', '-deps', '-json', './cmd/...'],
                                cwd=ROOT, env=env, capture_output=True, text=True, check=True)
        for package in json_stream(result.stdout):
            module = package.get('Module', {})
            if module and not module.get('Main'):
                modules[module['Path']] = module
    goroot = pathlib.Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    # Official distributions keep LICENSE in GOROOT; Homebrew moves it one
    # directory up next to libexec. Both locations belong to the Go installation.
    runtime_license = next((path for path in (goroot / 'LICENSE', goroot.parent / 'LICENSE')
                            if path.is_file()), None)
    if runtime_license is None:
        raise RuntimeError('Go installation LICENSE was not found')
    parts = ['Third-party notices for distributed Go executables\n',
             'Go runtime and standard library\n\n' + runtime_license.read_text()]
    for name, module in sorted(modules.items()):
        directory = pathlib.Path(module['Dir'])
        notices = [directory / filename for filename in
                   ('LICENSE', 'LICENSE.txt', 'LICENSE.md', 'COPYING', 'NOTICE')
                   if (directory / filename).is_file()]
        if not notices:
            raise RuntimeError('No license notice found for dependency ' + name)
        parts.append(name + ' ' + module.get('Version', '') + '\n\n' +
                     '\n\n'.join(path.name + '\n' + path.read_text() for path in notices))
    return '\n\n' + ('\n\n' + '=' * 72 + '\n\n').join(parts)


def package_files(folder, windows):
    names = [pathlib.Path(name) for name in DOCUMENTS]
    names.append(pathlib.Path('setup.ps1' if windows else 'setup.sh'))
    for name in names:
        source = ROOT / name
        destination = folder / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(source.read_bytes())
        destination.chmod(0o755 if name.name == 'setup.sh' else 0o644)


def archive(folder, destination, windows):
    files = sorted(path for path in folder.rglob('*') if path.is_file())
    if windows:
        with zipfile.ZipFile(destination, 'w', compression=zipfile.ZIP_DEFLATED) as out:
            for path in files:
                out.write(path, pathlib.Path(folder.name) / path.relative_to(folder))
    else:
        with tarfile.open(destination, 'w:gz') as out:
            for path in files:
                info = out.gettarinfo(str(path), str(pathlib.Path(folder.name) / path.relative_to(folder)))
                info.uid = info.gid = 0
                info.uname = info.gname = ''
                info.mtime = 0
                with path.open('rb') as stream:
                    out.addfile(info, stream)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True, help='Release version, e.g. v0.1.0')
    parser.add_argument('--targets', nargs='+', choices=TARGETS, default=TARGETS)
    parser.add_argument('--output', type=pathlib.Path, default=ROOT / 'dist')
    args = parser.parse_args()
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?', args.version):
        parser.error('version must look like v0.1.0 or v0.1.0-rc.1')
    destination = args.output.resolve() / args.version
    destination.mkdir(parents=True, exist_ok=True)
    notices = dependency_notices(args.targets)
    outputs = []
    for target in dict.fromkeys(args.targets):
        goos, goarch = target.split('/')
        windows = goos == 'windows'
        name = f'itmo-schedule-mcp_{args.version}_{goos}_{goarch}'
        extension = '.zip' if windows else '.tar.gz'
        with tempfile.TemporaryDirectory(prefix='itmo-release-') as temporary:
            folder = pathlib.Path(temporary) / name
            folder.mkdir()
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0', GOWORK='off')
            for command in ('itmo-calendar-sync', 'itmo-schedule-mcp', 'smoke'):
                flags = '-s -w'
                if command == 'itmo-calendar-sync':
                    flags += ' -X main.version=' + args.version
                binary = folder / (command + ('.exe' if windows else ''))
                subprocess.run(['go', 'build', '-trimpath', '-ldflags', flags,
                                '-o', str(binary), './cmd/' + command],
                               cwd=ROOT, env=env, check=True)
            package_files(folder, windows)
            (folder / 'THIRD_PARTY_NOTICES.txt').write_text(notices, encoding='utf-8')
            output = destination / (name + extension)
            archive(folder, output, windows)
            outputs.append(output)
            print(output.name, flush=True)
    manifest = ''.join(hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + path.name + '\n'
                       for path in sorted(outputs))
    (destination / 'SHA256SUMS').write_text(manifest, encoding='utf-8')
    print('SHA256SUMS', flush=True)


if __name__ == '__main__':
    main()
