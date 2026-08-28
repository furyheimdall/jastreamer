# jastreamer Server Administration Design System

## 0. Research Log

- Existing product contract: extended `web/pairing/DESIGN.md` so trust, typography, spacing, focus, and surface treatments remain recognizably jastreamer without changing `/pair/`.
- Embedded references: reused the pairing shortlist (Vercel, Linear, IBM) and its Vercel selection. Vercel contributes restrained monochrome hierarchy, shadow-as-border surfaces, and compressed headings; no branding or remote assets are copied.
- StyleGallery: adopted `fixed-sidenav-shell` for desktop administration. The document/main region owns vertical scroll; navigation remains contextual. Below 760px the shell becomes normal single-column document flow, with no nested scrolling.
- Interaction: consulted beui.dev `button`/`StatefulButton`. Adapted its idle/loading/success/error semantics, disabled pending state, `aria-busy`, and reduced-motion path to dependency-free CSS; no decorative motion was adopted.
- Imagen and Lazyweb: skipped. This is an extension of an established operational design contract, not a new visual direction, and the write scope excludes unrelated generated assets.

## 1. Atmosphere & Identity

A quiet local operations console: trustworthy, information-dense without becoming a cockpit, and visibly separate from playback Control. The signature is a narrow status rail that keeps server identity, restart state, and session state legible while an administrator works.

Design read: an embedded administration application for a technical operator, with restrained Vercel-derived precision and the existing pairing portal's local-trust language. Dials: variance 3, motion 2, density 6.

## 2. Color

The admin application uses the pairing semantic tokens unchanged: canvas, panel, primary/muted text, line, green functional accent, and red danger. It adds `--warning-surface` and `--warning-text` solely for restart/conflict state, plus `--success-surface` for connected/complete status. Dark values remain in the same cool-neutral family. Color never carries state alone; every state includes text.

## 3. Typography

Use local system sans and system monospace only; the embedded app makes no network font request. H1 is 2rem/600/1.1, H2 1.25rem/600/1.3, H3 1rem/600/1.4, body 1rem/400/1.55, small .875rem/1.45, and technical values .8125rem mono with tabular figures. Headings use restrained negative tracking. Long values wrap anywhere.

## 4. Spacing & Layout

Base unit is 4px. Scale: 4, 8, 12, 16, 24, 32, and 48px. Desktop uses a 15rem navigation rail and `minmax(0, 1fr)` content up to 1120px. The document is the only vertical scroll owner. Repeated grids use `repeat(auto-fit, minmax(min(18rem, 100%), 1fr))`. At 760px the rail becomes a horizontal wrapping navigation cluster and all content is one column. At 390px gutters are 16px and controls remain at least 44px tall.

## 5. Components

### Session Gate
- States: empty, validating (`aria-busy`), authenticated, invalid/expired.
- Token is a password input, cleared after submit, stored only in `sessionStorage`, and never rendered.

### Status Rail
- Structure: product identity, section links, session action.
- States: active section, restart required, session ended.
- Source order precedes main content and remains keyboard-linear.

### Settings Group
- Structure: heading, concise guidance, labeled controls, field-level helper/error, actions.
- States: clean, dirty, pending, saved, validation error, locked, stale conflict.
- Locked runtime values use disabled controls plus visible `Read only` text.

### Inventory List
- Used for roots, jobs, renderers, zones, and devices.
- Structure: semantic list, strong item name, status metadata, contextual control.
- States: loading, empty, populated, pending, error, revoked.

### State Banner
- Roles: restart warning, ETag conflict, inline API error.
- Banners use explicit headings/text and focus targets; they do not rely on color.

### Stateful Action
- States: idle, pending/disabled with `aria-busy`, success, error.
- Press feedback uses a 1px Y transform only. No spinner or automatic animation.

The product screen doubles as the primitive state harness: login/error, settings locked/conflict/restart, inventory loading/empty/populated, and action pending/error states are all driven in Playwright at desktop and mobile.

## 6. Motion & Interaction

Only 120ms opacity/transform feedback is allowed for actionable controls and navigation. Pending actions disable only the initiating control. Focus is immediate and uses a 3px accent outline. `prefers-reduced-motion` removes transforms and transitions. There is no automatic, entrance, scroll, or decorative motion.

## 7. Depth & Surface

Use the established mixed Vercel-derived treatment: a zero-offset shadow border plus one shallow green-tinted ambient layer for primary panels. Inner groups are separated by spacing or single hairlines rather than nested cards. Radius is 8px for panels, 6px for controls, and full pill only for compact status labels.

## 8. Accessibility Constraints & Accepted Debt

WCAG 2.2 AA constraints: 4.5:1 body contrast, visible focus, semantic landmarks/headings/lists, explicit labels, `aria-live` for operation outcomes, `aria-invalid` for failed inputs, keyboard-complete workflows, 44px targets, and 390px reflow without horizontal document scrolling. Destructive actions include the target name. Authentication failure returns focus to the login error; ETag conflict exposes one explicit refresh/reapply action.

| Item | Location | Why accepted | Owner / Exit |
| --- | --- | --- | --- |
| Scan history is session-local | Catalog jobs | The accepted API exposes lookup by known job ID but no job-list route; the UI tracks jobs it starts without changing API semantics | Replace with authoritative history when a list endpoint is accepted |
| UPnP/PCM status is derived | Renderer and audio status | Current APIs expose renderer kind/capabilities and FFmpeg configuration, but no separate probe-status endpoint | Replace derived labels when Todo 10/13 status fields ship |
