#!/usr/bin/env python3
"""Compare fresh Go/TypeScript bindings without overwriting the working files."""
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix='pastime-contract-') as temporary:
    target = Path(temporary)
    subprocess.run(['python3', 'tools/api-contract.py', '--output', str(target / 'openapi.yaml')], cwd=root, check=True)
    if (target / 'openapi.yaml').read_bytes() != (root / 'internal/httpapi/openapi.yaml').read_bytes():
        raise SystemExit('OpenAPI source is stale; run make generate')
    subprocess.run(['go', 'tool', 'oapi-codegen', '-generate', 'types', '-package', 'httpapi',
                    '-o', str(target / 'types.gen.go'), 'internal/httpapi/openapi.yaml'], cwd=root, check=True)
    subprocess.run(['pnpm', 'exec', 'openapi-typescript', 'internal/httpapi/openapi.yaml',
                    '-o', str(target / 'schema.gen.ts')], cwd=root, check=True)
    for generated, current in [('types.gen.go', 'internal/httpapi/types.gen.go'),
                               ('schema.gen.ts', 'web/src/api/schema.gen.ts')]:
        if (target / generated).read_bytes() != (root / current).read_bytes():
            raise SystemExit(f'{current} is stale; run make generate')
print('Generated Go and TypeScript contracts are current.')
