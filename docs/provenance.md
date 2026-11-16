# Licence and authorship

## Licence

Pastime is licensed under the GNU Affero General Public License, version 3 only (AGPL-3.0-only). See [LICENSE](../LICENSE). Third-party dependencies and assets keep their own licences, listed in the [dependency inventory](../third_party/README.md). Pastime's licence does not override them.

If you run a modified Pastime for other people over a network, section 13 of the AGPL requires you to offer them the source of your modified version.

## Authorship

Pastime is an independently implemented project. It is not a port of Floppy (a Python media tracker, [Bloem-Studios/Floppy](https://github.com/Bloem-Studios/Floppy)) or of [Silo](https://github.com/Silo-Server/silo-server). Floppy informed which behaviours a media tracker needs, and Silo informed the technology, the design direction and the watch-sync contract that Bloem Server uses to talk to Pastime. Pastime is written in Go and TypeScript, and its API is its own.

Floppy and Silo are also AGPL-3.0, so reusing their code is licence-compatible. Pastime prefers its own implementation, and any reuse follows these rules:

1. Code, tests, schemas, templates, CSS or assets from an AGPL-3.0 project such as Floppy or Silo may be reused only with their copyright notices kept, an entry in `third_party/NOTICES.txt` and a commit message naming the source and revision. Material under any other licence needs its own clearance.
2. Author the application, migrations, UI components, schemas and tests independently. The syntax and behaviour of public protocols are requirements to characterise, not permission to copy an implementation.
3. Keep external-integration inventories factual: paths, request and response fields, authorisation behaviour, status codes and observable outcomes.
4. Use synthetic users, media records and event sequences in development and tests. Never put production data in the repository.
5. Install libraries from their original distribution, not copied out of another application.
6. Keep a dependency and asset inventory with exact versions, licence texts and notices, including transitive Go and npm dependencies, fonts, icons and build tools.
7. Use no other product's logo or unlicensed poster artwork. Provider API terms and artwork rights are separate from software licences.

Pastime is not a formal clean-room implementation, and nothing here claims it is. It contains no Floppy or Silo code, tests, assets or Git history.

## Limits of this page

This is an engineering policy, not a legal opinion about API copyright, patents, trademarks, database rights or any jurisdiction. Automated licence scanners do not give legal clearance. Pastime is a working name.

The [dependency inventory](../third_party/README.md) covers application and tooling notices. [Host recovery tools](../third_party/host-tools/README.md) and container images have separate distribution terms.
