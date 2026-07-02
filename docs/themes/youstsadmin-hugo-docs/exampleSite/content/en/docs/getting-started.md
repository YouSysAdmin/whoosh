---
title: "Getting started"
description: "Preview the theme standalone from its exampleSite."
weight: 10
lastmod: 2026-03-01
---

This page is rendered by `single.html` - note the reading-time meta in the header,
the on-this-page table of contents (when the page has enough headings), and the
prev/next pager at the bottom.

## Preview the theme

Run the bundled example site from inside the theme:

```bash
cd exampleSite
hugo server --themesDir ../..
```

Then open <http://localhost:1313/>.

## Configure

The home page, hero terminal, feature grid, footer and menu are all driven from
`hugo.toml` `[params]` and `[[menu.main]]`. Menu entries and sections can carry an
`icon` (see the [shortcodes](../shortcodes/) page for the icon pack).

## Write content

Drop Markdown under `content/`. Top-level folders become sidebar sections (give
each `_index.md` a `weight` and an optional `icon`). Set `sidebar: false` on a
section to keep it out of the sidebar (the changelog does this).
