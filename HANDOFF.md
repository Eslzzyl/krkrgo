# krkrgo Handoff

Status: 2026-10-14
Repository: `/Users/eslzzyl/WorkSpace/Go/krkrgo` (macOS) and `D:/WorkSpace/Go/krkrgo` (Windows)

## Current objectives

Deferred follow-up: after the TJS2100 migration and startup verification are complete, rename krkrgo's internal `legacy` files, symbols, comments, tests, and documentation to the official `InterCode`/`TJS2100` terminology. Preserve behavior during the rename.

## GPU composition status

The interactive Ebitengine path now defaults to GPU composition, with `-renderer gpu|cpu` on `krkrgo run`. The CPU compositor remains available through `NewCPUGame`, GPU initialization fallback, and headless replay.

The GPU composition logic may still be incomplete relative to the CPU semantic reference. Animation and transition coverage received follow-up fixes, but broader visual parity remains unverified across project content and supported layer combinations.

GPU composition performance has not been measured. No benchmark, frame-time comparison, memory profile, or driver/platform comparison is currently available.
