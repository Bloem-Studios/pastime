# Documentation

| Page | Purpose |
| --- | --- |
| [Features](features.md) | What Pastime does, area by area |
| [Configuration](configuration.md) | Every environment variable, with defaults and rules |
| [Development](development.md) | Local setup, workers, tests, code generation and operating notes |
| [Silo sync](integrations/silo.md) | How the Bloem Pastime plugin talks to Pastime, the Connect Silo page and sync-key lifetime |
| [Managed tracking](managed-tracking.md) | Household profiles, identity claims and the native Bloem endpoints |
| [Deployment](../deploy/lxc/README.md) | Single-host installer, file layout and maintenance |
| [Recovery](../deploy/lxc/RECOVERY.md) | Encrypted backups, key rotation and isolated restore checks |
| [Interface design](design/README.md) | The interface and its accessibility coverage |
| [Usability acceptance](../tests/browser/USABILITY.md) | The synthetic populated browser suite |
| [Licence and authorship](provenance.md) | Licence, independent authorship and reuse rules |
| [Third-party inventory](../third_party/README.md) | Dependency versions and retained notices |

The code and its tests are the final authority. Tests use synthetic fixtures, so a passing test run does not show that a live provider or service still behaves as the adapters expect.
