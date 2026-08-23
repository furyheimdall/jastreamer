# Jake Streamer Pairing Portal Design System

## 0. Research Log

- Embedded refs: shortlisted Vercel, Linear, and IBM; picked the operational `taste-skill` with Vercel because certificate identity and device administration benefit from quiet monochrome precision.
- Lazyweb: one desktop query returned four security/admin screens; one Frontegg privacy-and-security screen was viewed. Adopted its clear task area plus separately readable identity/device regions, without copying branding or layout chrome.
- StyleGallery: adopted `content-limiter`, `stack`, and `cluster`; the document owns vertical scroll and all task groups collapse to one column on narrow screens.
- Imagen drafts: skipped because no image-generation tool is available and an administrative pairing form has no meaningful imagery requirement.

## 1. Atmosphere & Identity

A quiet local trust ceremony: precise, calm, and visibly separate from the music controller. The signature is the certificate fingerprint rendered as a deliberate identity strip before any administrative action.

## 2. Color

| Role | Token | Light | Dark | Usage |
| --- | --- | --- | --- | --- |
| Canvas | `--surface-canvas` | `#f7f7f5` | `#111210` | Document |
| Panel | `--surface-panel` | `#ffffff` | `#1b1c19` | Task regions |
| Text | `--text-primary` | `#171717` | `#f3f3ef` | Primary copy |
| Muted | `--text-muted` | `#5f625d` | `#b5b7b1` | Guidance |
| Line | `--line` | `rgba(0,0,0,.10)` | `rgba(255,255,255,.14)` | Shadow borders |
| Accent | `--accent` | `#1c5f47` | `#66c79f` | Actions and focus |
| Danger | `--danger` | `#a22929` | `#ff8b85` | Revocation and errors |

Accent is functional only. No workflow or media colors are introduced.

## 3. Typography

| Level | Size | Weight | Line height | Usage |
| --- | --- | --- | --- | --- |
| H1 | `2rem` | 600 | 1.15 | Portal title |
| H2 | `1.25rem` | 600 | 1.3 | Task heading |
| Body | `1rem` | 400 | 1.55 | Labels and guidance |
| Small | `.875rem` | 400 | 1.45 | Status and metadata |
| Mono | `.875rem` | 500 | 1.5 | Fingerprint and code |

Primary uses the local system sans stack; identity values use the local system monospace stack. No remote font request is permitted.

## 4. Spacing & Layout

Base unit is 4px. Tokens: `--space-1` 4px, `--space-2` 8px, `--space-3` 12px, `--space-4` 16px, `--space-6` 24px, `--space-8` 32px, `--space-12` 48px. Content is limited to 880px with 16px mobile gutters. The document is the only scroll owner.

## 5. Components

### Identity Strip
- Structure: heading, trust guidance, fingerprint output.
- States: loading, available, error; long values wrap anywhere.
- Accessibility: output is live but not interruptive.

### Task Panel
- Structure: heading, guidance, labeled form controls, status output.
- States: default, focus, pending/disabled, success, inline error.
- Layout: stack; action rows use cluster and wrap before overflow.

### Device Row
- Structure: device name, role/status metadata, revoke button.
- States: active, revoked, pending, error, empty list.
- Accessibility: destructive action names the target device.

## 6. Motion & Interaction

Only 120ms transform/opacity feedback is allowed on buttons. Focus is immediate. Reduced-motion removes the press transform. There is no decorative or automatic motion.

## 7. Depth & Surface

Mixed strategy: Vercel-derived shadow-as-border for panels plus one shallow tinted elevation layer. Radius is 8px for panels and 6px for controls; status codes are the only pill form.

## 8. Accessibility Constraints & Accepted Debt

WCAG 2.2 AA: 4.5:1 body contrast, visible focus, semantic labels, keyboard-complete forms, 44px minimum action height, and mobile reflow without horizontal document scrolling.

| Item | Location | Why accepted | Owner / Exit |
| --- | --- | --- | --- |
| No visual certificate QR | Identity strip | Text fingerprint is safer and sufficient for the local bootstrap scope | Add only with a verified cross-device trust flow |
