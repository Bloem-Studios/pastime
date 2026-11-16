# Interface design

Pastime uses an independently authored, artwork-led interface informed by cinema-oriented media clients such as Silo. It contains no Silo source, branding or assets. See the [authorship policy](../provenance.md).

- **Colours.** Dark chrome uses charcoal (`#101416`), layered surfaces, teal primary actions (`#00d8ac`) and cyan focus/brand accents (`#06bce0`). Artwork supplies additional colour. The light theme uses a pale ground and darker teal/cyan for contrast. Dark is the default; people can choose light or follow the system.
- **Type:** Outfit (self-hosted, OFL). Hierarchy comes from size and colour, not weight: headings at 600 at most, sentence-case labels without wide tracking, and muted metadata. Headings and empty-state titles carry no trailing full stop.
- **Shape:** 12 px artwork and panel corners, and pills for actions, chips and filters. Borders are one step off the surface. Shadows are reserved for overlays.
- **Icons:** Lucide at 1.75 px strokes. Each icon names what its action does; no decorative sparkles.
- **Layout:** a collapsible 260 px sidebar with grouped navigation, and a bottom bar on phones.
  - Home and title pages open on full-bleed artwork under a transparent header that turns solid on scroll.
  - Discovery happens in horizontal shelves of poster (2:3) and backdrop (16:9) cards.
  - Library/search grids use bounded API pages. Discovery appends pages as the sentinel approaches the viewport; failures offer retry and the end of results is explicit.
  - The profile chooser stays readable on narrow phones and separates household libraries without changing login identity.
- **Administration** is a separate console: dashboard, jobs, providers, catalog, imports, users, invitations, audit, and the API reference. It has ⌘K section search and a live queue indicator.
- **Motion** is CSS only: hover is a brightness and hairline change on artwork (nothing lifts or scales), a transform-only page settle and a slow zoom on the Home backdrop. It is disabled under reduced motion. Nothing animates opacity on page entry, so contrast checks stay valid.
- **Performance:**
  - Every route is its own lazily loaded chunk; bundle sizes are build-dependent and should be read from the current Vite output.
  - Images are lazy with fixed aspect ratios.
  - Shelves use `content-visibility`.
  - The only fonts are the two Outfit subsets.

The browser suite checks twenty workspace routes with WCAG 2 A/AA axe scans and horizontal overflow at 1366 px and 390 px, including German on mobile. The browser suite also covers session cancellation, bulk-selection disclosure and Swagger. Automated scans are not an exhaustive accessibility audit.

