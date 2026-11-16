#!/usr/bin/env python3
"""Create a local-only development environment, without overwriting an existing file."""
import os
from pathlib import Path
import secrets

root = Path(__file__).resolve().parents[1]
password = secrets.token_hex(24)
secret_key = __import__('base64').b64encode(secrets.token_bytes(32)).decode()
data = (
    f'POSTGRES_PASSWORD={password}\n'
    'POSTGRES_PORT=18543\n'
    'VALKEY_PORT=18679\n'
    f'DATABASE_URL=postgres://pastime:{password}@127.0.0.1:18543/pastime_dev?sslmode=disable\n'
    f'PASTIME_TEST_DATABASE_URL=postgres://pastime:{password}@127.0.0.1:18543/pastime_test?sslmode=disable\n'
    f'SECRET_KEY={secret_key}\n'
    'VALKEY_URL=redis://127.0.0.1:18679/0\n'
    'PASTIME_TEST_VALKEY_URL=redis://127.0.0.1:18679/0\n'
    'LISTEN_ADDR=127.0.0.1:8080\n'
)
try:
    fd = os.open(root / '.env', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    raise SystemExit('.env already exists; refusing to overwrite it')
with os.fdopen(fd, 'w') as target:
    target.write(data)
print('Created private development .env. No services were started.')
