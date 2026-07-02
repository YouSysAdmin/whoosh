---
title: "Shortcodes"
description: "Callouts, tooltips and inline icons shipped with the theme."
weight: 20
---

## Callouts

{{< callout >}}
A default **note**. The body is Markdown - `code`, [links](#) and lists all work.
{{< /callout >}}

{{< callout type="info" >}}
Informational context the reader should know.
{{< /callout >}}

{{< callout type="tip" title="Pro tip" >}}
Pass a custom `title` to override the default heading.
{{< /callout >}}

{{< callout type="success" >}}
Something completed successfully.
{{< /callout >}}

{{< callout type="warning" >}}
Be careful - this needs attention.
{{< /callout >}}

{{< callout type="danger" icon="alert-octagon" >}}
Destructive or irreversible action.
{{< /callout >}}

### Usage

```text
{{</* callout type="warning" title="Heads up" */>}}
Body in **markdown**.
{{</* /callout */>}}
```

Types: `note` (default), `info`, `tip`, `success`, `warning`, `danger`
(aliases: `warn`, `caution`/`error`, `important`). Pass it positionally
(`{{</* callout warning */>}}`), set `title=""` to hide the heading, or override
the glyph with `icon="<name>"`.

## Tooltips

Hover or keyboard-focus {{< tooltip text="Command-line interface" >}}CLI{{< /tooltip >}}
to see a bubble. A reusable {{< tooltip term="TOML" >}}TOML{{< /tooltip >}}
definition is pulled from `data/tooltips.toml`.

```text
{{</* tooltip text="Command-line interface" */>}}CLI{{</* /tooltip */>}}
{{</* tooltip term="TOML" */>}}TOML{{</* /tooltip */>}}
```

## Icons

Inline an icon anywhere: {{< icon rocket >}} ship it, {{< icon github >}} source.

```text
{{</* icon rocket */>}}
```

Bundled names include `info`, `lightbulb`, `rocket`, `terminal`, `book-open`,
`download`, `clock`, `github`, `check-circle`, `alert-triangle`. Add more by
dropping an SVG (using `stroke="currentColor"`) into `assets/icons/`.
