---
title: "Configuration"
description: "Where the theme reads its settings."
weight: 10
---

Most of the theme is configured from `hugo.toml`:

## Params

- `brand`, `tagline`, `description` - identity shown in the nav, hero and footer.
- `trust_badges`, `terminal_lines`, `[[params.features]]` - home page content.
- `repoURL`, `show_footer_project` - the footer "Project" column.
- `cta_url`, `cta_label`, `cta_icon` - the nav call-to-action button. Set
  `cta_url` to show it; `cta_label` defaults to "Get started" and `cta_icon`
  optionally renders a leading icon (any name from the icon pack, e.g. `github`).

## Menu

`[[menu.main]]` entries render in the top nav. Add `[menu.main.params]` with an
`icon` to show a glyph; absolute URLs open in a new tab automatically.

## Sections

Each section's `_index.md` takes a `weight` (sidebar order), an optional `icon`,
and `sidebar: false` to hide it from the sidebar.
