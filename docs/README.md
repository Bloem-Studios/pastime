# Documentation

Where to start:

| Page | Purpose |
| --- | --- |
| [Development](development.md) | Local installation, owner bootstrap, workers, tests and code generation |
| [Configuration](configuration.md) | Runtime, provider and test environment variables |
| [Deployment](../deploy/lxc/README.md) | Single-host installer, file layout and maintenance |
| [Recovery](../deploy/lxc/RECOVERY.md) | Encrypted backups, key rotation and isolated restore checks |
| [Provenance](provenance.md) | Licence, independent authorship and reuse rules |
| [Third-party inventory](../third_party/README.md) | Exact dependency versions and retained notices |

Features and integrations:

- [Silo sync](integrations/silo.md): how the Bloem Pastime plugin talks to Pastime, the Connect Silo page and sync-key lifetime.
- [Managed tracking](managed-tracking.md): household profiles, identity claims and the native Bloem endpoints.
- [September capabilities](september-capabilities.md) and [October capabilities](october-capabilities.md): saved views, rating evidence, imports, linked services, extra catalog sources and device-code sign-in.
- [Interface design](design/README.md): the implemented interface and accessibility coverage.

The code and its tests are the final authority. Tests use synthetic fixtures, so a passing test run does not show that a live provider or service still behaves as the adapters expect.
