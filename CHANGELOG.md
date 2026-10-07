# Changelog

## 1.0.0 — 2026-10-07

First release.

- htop-style header: swap split into process-owned, swap cache (reserved but
  in RAM) and tmpfs/shm; zswap and zram figures; per-device usage.
- Per-process SWAP, SWPPSS, DELTA, RSS and SWAPPED columns; per-mapping
  breakdown; search, sort, tree view.
- Actions: swap a process in, page a process out, flush every swap device,
  send a signal.
- Settings persist in `~/.config/swaptop/config`.
