#!/usr/bin/env python3
"""Collect exact installed dependency notices; this is inventory, not legal clearance."""
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def command(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True)


def objects(text):
    decoder = json.JSONDecoder()
    while text.strip():
        text = text.lstrip()
        value, end = decoder.raw_decode(text)
        yield value
        text = text[end:]


def licenses(directory):
    return sorted(p for p in directory.iterdir() if p.is_file() and
                  p.name.lower().startswith(('license', 'licence', 'copying', 'copyright', 'notice')))


runtime = {p['Module']['Path'] for p in objects(command('go', 'list', '-deps', '-json', './cmd/pastime'))
           if p.get('Module') and not p['Module'].get('Main')}
project_tests = {p['Module']['Path'] for p in objects(command('go', 'list', '-deps', '-test', '-json', './...'))
                 if p.get('Module') and not p['Module'].get('Main')}
tools = json.loads(command('go', 'mod', 'edit', '-json')).get('Tool', [])
tool_modules = set()
for tool in tools:
    tool_modules.update(p['Module']['Path'] for p in objects(command('go', 'list', '-deps', '-json', tool['Path']))
                        if p.get('Module') and not p['Module'].get('Main'))
used_modules = runtime | project_tests | tool_modules
notices = ['THIRD-PARTY NOTICES\n\nIncludes runtime and development/build dependencies.\n'
           "Original terms apply; Pastime's AGPL-3.0 licence does not replace them.\n"]
inventory = []
missing = []


def record(kind, name, version, source, files, extra=None):
    if not files:
        missing.append(f'{name}@{version}')
    row = {'kind': kind, 'name': name, 'version': version, 'source': source, 'licenses': []}
    if extra:
        row.update(extra)
    for path in files:
        data = path.read_bytes()
        row['licenses'].append({'file': path.name, 'sha256': hashlib.sha256(data).hexdigest()})
        notices.append(f'\n{"=" * 72}\n{name}@{version} [{kind}] - {path.name}\n{source}\n\n' + data.decode('utf-8'))
    inventory.append(row)


for module in objects(command('go', 'list', '-m', '-json', 'all')):
    if module.get('Main') or module['Path'] not in used_modules:
        continue
    if 'Dir' not in module:
        module.update(json.loads(command('go', 'mod', 'download', '-json', module['Path'] + '@' + module['Version'])))
    record('go-runtime' if module['Path'] in runtime else 'go-test-or-tool', module['Path'], module['Version'],
           f'https://pkg.go.dev/{module["Path"]}@{module["Version"]}', licenses(Path(module['Dir'])),
           {'module_sum': module.get('Sum', '')})

for declared_license, packages in json.loads(command('pnpm', 'licenses', 'list', '--json')).items():
    for package in packages:
        for path in package['paths']:
            directory = Path(path)
            metadata = json.loads((directory / 'package.json').read_text())
            notice_files = licenses(directory)
            if not notice_files:
                fallback = ROOT / 'third_party/license-sources' / package['name'] / metadata['version']
                if fallback.is_dir():
                    notice_files = licenses(fallback)
            record('node-development', package['name'], metadata['version'],
                   f'https://www.npmjs.com/package/{package["name"]}/v/{metadata["version"]}',
                   notice_files, {'declared_license': declared_license})

swagger = json.loads((ROOT / 'third_party/swagger-ui-dist/manifest.json').read_text())
record('bundled-runtime', 'swagger-ui-dist', swagger['version'], swagger['source'],
       licenses(ROOT / 'third_party/swagger-ui-dist') + [ROOT / 'internal/httpapi/assets/swagger-ui-bundle.js.LICENSE.txt'],
       {'integrity': swagger['integrity'], 'declared_license': 'Apache-2.0'})
goroot = Path(command('go', 'env', 'GOROOT').strip())
go_license = goroot / 'LICENSE'
if not go_license.is_file():
    go_license = goroot.parent / 'LICENSE'  # Homebrew keeps notices above libexec.
record('toolchain-and-stdlib', 'Go', command('go', 'env', 'GOVERSION').strip(), 'https://go.dev/', [go_license])
if missing:
    raise SystemExit('Missing license files require review: ' + ', '.join(missing))

(ROOT / 'third_party/NOTICES.txt').write_text('\n'.join(notices))
(ROOT / 'third_party/inventory.json').write_text(json.dumps(sorted(inventory, key=lambda r: (r['kind'], r['name'], r['version'])), indent=2) + '\n')
print(f'Collected notices for {len(inventory)} dependency records; no legal-clearance claim.')
