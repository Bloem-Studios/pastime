# Third-party inventory

`inventory.json` records exact versions and notice hashes for modules used by the executable, project tests and generators, plus Node development dependencies and the bundled Swagger distribution. `NOTICES.txt` retains their notices and is embedded in the executable (`pastime licenses`) and exposed at `/docs/licenses.txt`.

Regenerate with `make licenses` after installing the pinned Go and pnpm dependencies. The collector does not copy dependency implementation source. Module references in `go.sum` that belong only to upstream projects' own unused test graphs are not executable/project-test/generator dependencies and are not represented as shipped code.

This is an engineering inventory, not legal clearance. Source rights, fonts/artwork, commercial terms and later dependencies remain subject to review. No Floppy/Silo application source or upstream history is included.

## Swagger

`swagger-ui-dist/manifest.json` identifies npm distribution 5.33.0, package integrity and the unmodified bundled JS/CSS hashes. The Apache license, notice and bundle license notices are retained. Assets are self-hosted; external schema validation and authorization persistence are disabled. Third-party assets remain under their original terms, not the repository's AGPL v3 licence.

## Missing npm notice files

The npm archives for `@redocly/openapi-core@1.34.20` and `change-case@5.4.4` omit their root license files. `license-sources/` retains those files from each package's declared exact Git commit, with source/blob provenance.

The published `uri-js-replace@1.0.1` archive and its declared source commit omit a license file. Rather than infer terms from a license added later, the development-only Redocly AJV dependency is explicitly aliased to the original `uri-js@4.4.1`, whose BSD license is included. Native OpenAPI TypeScript generation is tested with that override. The pinned Node dependency audit reported no known vulnerabilities at the initial foundation check; repeat audits for updates.

The platform-specific Rolldown 1.2.9 native binding archives omit their license file. The Darwin ARM64 and Linux x64 GNU fallback notices are retained from the same pinned upstream release commit, with exact provenance in each `source.json`. The generated inventory reflects installed platform dependencies; regenerate on the pinned Linux CI baseline for the committed release inventory.

## Container/tooling boundary

`compose.dev.yaml` pins PostgreSQL 18.4 and Valkey 8.1.10 by multiarchitecture digest. These are external development services, not code copied into the Go executable. Their images contain their own OS/package terms; shipping a production appliance requires a separate container/SBOM/distribution review. The repository does not claim that every component of a container image inherits PostgreSQL's or Valkey's primary license.

Go module notices are MIT/BSD/Apache-family texts in the current inventory; Node metadata also includes ISC, Python-2.0 and a MIT-or-CC0 choice. Keep original notices and license choices visible.

## Application assets and recovery tools

The React dependencies, self-hosted Outfit font and Lucide icons are pinned in `package.json` / `pnpm-lock.yaml` and represented in the generated inventory/notices. Do not infer provider artwork rights from those software licenses. Regenerate notices whenever the dependency graph changes; documentation-only edits do not change the graph.

The deployment and [recovery scripts](../deploy/lxc/RECOVERY.md) invoke external host tools. [Host-tool notices](host-tools/README.md) record the synthetic rehearsal's GnuPG package and licensing details. PostgreSQL 18.4 restoration was rehearsed locally; this is operational evidence, not appliance-distribution clearance.
