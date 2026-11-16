"""Execute the installer against disposable paths and recorded service commands."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class InstallOrdering(unittest.TestCase):
    def rehearse(self, fail_migration=False):
        with tempfile.TemporaryDirectory(prefix='pastime-install-test-') as temporary:
            root = Path(temporary)
            for directory in ('etc/pastime', 'opt/pastime', 'usr/local/bin', 'mocks'):
                (root / directory).mkdir(parents=True)
            (root / 'etc/pastime/compose.env').write_text('POSTGRES_PASSWORD=synthetic\n')
            (root / 'etc/pastime/pastime.env').write_text('SECRET_KEY=synthetic\n')
            (root / 'usr/local/bin/pastime.new').write_text('synthetic binary\n')
            (root / 'opt/pastime/runtime-role.sql').write_text('SELECT 1;\n')
            mock = root / 'mocks/command'
            mock.write_text('''#!/bin/sh
name=${0##*/}
printf '%s %s\\n' "$name" "$*" >> "$EVENTS"
case "$name" in
 systemd-run) [ "$FAIL_MIGRATION" != 1 ] || exit 1;;
 docker) case "$*" in *exec*) cat >/dev/null;; esac;;
esac
exit 0
''')
            mock.chmod(0o755)
            for name in ('id', 'install', 'chgrp', 'systemctl', 'systemd-run', 'docker', 'curl'):
                (root / 'mocks' / name).symlink_to(mock)
            script = (ROOT / 'deploy/lxc/install.sh').read_text()
            for prefix in ('/etc/pastime', '/opt/pastime', '/usr/local/bin', '/etc/systemd/system', '/var/lib/pastime', '/var/backups/pastime'):
                script = script.replace(prefix, str(root) + prefix)
            path = root / 'install.sh'
            path.write_text(script)
            env = dict(os.environ, PATH=str(root / 'mocks') + ':' + os.environ['PATH'],
                       EVENTS=str(root / 'events'), PUBLIC_ORIGIN='http://example.invalid',
                       FAIL_MIGRATION='1' if fail_migration else '0')
            result = subprocess.run(['sh', str(path)], env=env, capture_output=True, text=True)
            return result, (root / 'events').read_text().splitlines()

    def test_services_stop_before_dependencies_and_restart_after_migration(self):
        result, events = self.rehearse()
        self.assertEqual(result.returncode, 0, result.stderr)
        def index(fragment):
            return next(i for i, event in enumerate(events) if fragment in event)
        self.assertLess(index('stop pastime-worker.service'), index('stop pastime.service'))
        self.assertLess(index('stop pastime.service'), index('up -d --wait'))
        self.assertLess(index('up -d --wait'), index('systemd-run'))
        self.assertLess(index('systemd-run'), index('start pastime.service'))
        self.assertLess(index('curl -fsS'), index('start pastime-worker.service'))

    def test_failed_migration_leaves_services_stopped(self):
        result, events = self.rehearse(True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(event.startswith('systemctl start ') for event in events))


if __name__ == '__main__':
    unittest.main()
