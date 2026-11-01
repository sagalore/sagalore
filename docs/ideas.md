# Ideas (parked)

Features that exist or are sketched, but are not in the current “make the basics solid” focus.

## Saga chains (`requires` + `chain`)

**Status:** Implemented in API, sync, and addon lock UI. Premature to lean on for playtests.

**Why parked:** Onboarding, emotes, Discord, character opt-in, and Classic reliability matter more. Chains add authoring and QA surface before the happy path is boring.

**When to pick up:** After emote/location/interact flows and companion identity feel stable; then run the chain test plan (A unlocks B over WS).

See README section *Saga-kjeder* and [API.md](API.md).

## Character opt-in

**Status:** Design only — [character-opt-in.md](character-opt-in.md).

Discover ≠ enroll; addon prompt. Do this before inventing more placeholder characters in local DB.

## Timed sequences between objectives

**Status:** Built as one `sequence` step (`within_sec` + `beats`), not as saga chains. See [API.md](API.md).

## Live top-level location ticks

**Status:** Explicit `memory`. `go` defaults to `visit` (stays ticked). `memory: "hold"` clears when you leave. `item` defaults to `hold`.
