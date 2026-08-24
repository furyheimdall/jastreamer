# jastreamer Control Design System

## 0. Research Log (greenfield only)
- Embedded refs: shortlisted operational patterns from the curated design library; picked `taste-skill` + tonal-shift control-room direction because playback, pairing, and policy need high legibility.
- StyleGallery: adopted the fixed-header shell / single bounded body scroll pattern; the body owns vertical scrolling.
- Skipped lanes: lazyweb and Imagen — no network/image-generation dependency is needed for a utility playback controller.

## 1. Atmosphere & Identity
A quiet listening room for a device that should feel dependable. jastreamer uses layered charcoal surfaces and one warm amber signal for playback intent; the signature is the amber “needle” that separates what is queued from what will happen next.

## 2. Color
| Role | Token | Value | Usage |
|---|---|---|---|
| Surface/primary | `surfacePrimary` | `#121315` | App background |
| Surface/secondary | `surfaceSecondary` | `#1B1D20` | Cards and navigation |
| Surface/elevated | `surfaceElevated` | `#24272B` | Dialogs and selected rows |
| Text/primary | `textPrimary` | `#F4F0E8` | Headings and controls |
| Text/secondary | `textSecondary` | `#B4B0A8` | Supporting copy |
| Text/tertiary | `textTertiary` | `#7D7B76` | Metadata |
| Accent/primary | `accentPrimary` | `#E9A23B` | Playback and primary actions |
| Status/success | `statusSuccess` | `#62C391` | Paired/ready |
| Status/warning | `statusWarning` | `#E9A23B` | Stale/incomplete |
| Status/error | `statusError` | `#E8756B` | Blocked/errors |
| Border/subtle | `borderSubtle` | `#626872` | 3:1 card and divider boundaries |
| Progress/track | `progressTrack` | `#3D4147` | Subdued analysis track behind amber value |
| Surface/danger | `surfaceDanger` | `#3A2424` | Unavailable explicit head |

## 3. Typography
Primary: system sans (Roboto on Android, Segoe UI on Windows, system on Web). Mono: system monospace for protocol/status labels. Body minimum 14px equivalent. Type scale: title 28/700, section 20/700, card 16/700, body 15/400, caption 12/600.

## 4. Spacing & Layout
Base unit 4px. Tokens: 4, 8, 12, 16, 24, 32, 48. The single Control room is centered at a 1120px maximum width on desktop and remains one column on smaller screens. The shell is viewport-bounded; only the `CustomScrollView` content body scrolls. No navigation rail is used because Todo14 defines one destination.

## 5. Components
### SurfaceCard
Tonal-shift panel with 16px padding and 12px radius. States: default, selected, stale, blocked. Uses semantic labels. The primary Discover action reserves a transparent 2px border and changes it to a high-contrast light focus ring during keyboard focus, without layout shift.
### PolicyChoice
Three-way segmented control for `stop`, `album`, and `similar`; selected state uses amber fill, unselected states use surface elevation. Keyboard and screen-reader selectable.
### QueuePreview
Two explicitly titled sections: “Explicit queue” and “Automatic next preview”. The preview is never represented as a queue row.
### StatusBanner
Paired, stale revision, coverage, and blocked-head variants with icon, heading, explanation, and next action.
### ServerCard
Discovered identity, HTTPS origin, SHA-256 fingerprint, pairing status, and one Server-advertised pairing action. States: available, pairing, paired, failed.
### PairingCompletion
External Server portal launch followed by explicit one-time controller-token paste. The obscured token exists only in session memory and never in a URL, history entry, screenshot, log, or persisted store.
### PolicySaveStatus
Server-confirmed revision and saved/unsaved state. A stale response rebases effective Server policy while preserving desired intent for an explicit retry; optimistic state is never labeled saved.

## 6. Motion & Interaction
Use Material transitions at 200ms. Interactive state changes use transform/opacity-equivalent Material feedback only. No decorative loops. Reduced motion follows platform accessibility settings.

## 7. Depth & Surface
Tonal-shift only: no drop shadows. Elevation is communicated by surface colors and the amber selection rail.

## 8. Accessibility Constraints & Accepted Debt
WCAG 2.2 AA target; body contrast >= 4.5:1, visible focus, semantic button/selection labels, 48px-equivalent touch targets, natural Korean wrapping, and no color-only status meaning. Pairing, policy save, stale recovery, and coverage changes use live-region text.

Accepted debt:
- Todo13 has no safe callback/token-exchange endpoint, so pairing completion is an explicit one-time paste instead of an automatic deep link. A future Server contract may replace this only with a single-use, non-secret callback handle.
- Web cannot inspect peer certificate bytes; browser trust plus visible fingerprint comparison is required. Windows/Android use an empty native trust store and accept only the exact discovered SHA-256 pin.
- Windows runner/MSIX is compile-config validated on Linux; executable and MSIX runtime verification remains Windows CI-owned.
