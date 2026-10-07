# swaptop

htop-style view of who is using swap and how. Linux only.

    go build -o swaptop . && sudo ./swaptop

Without root you only see full detail for your own processes, and the actions need root.

Header: swap bar split into green (owned by processes), yellow (swap cache: in RAM *and* holding a swap slot, i.e. reserved but not really swapped out) and magenta (tmpfs/shm, not owned by a process). zswap and zram figures appear when present.

Columns: SWAP (in swap now), SWPPSS (proportional share of shared pages), DELTA (change since last refresh), RSS, SWAPPED (share of the process that is in swap).

Keys: `Enter` per-mapping breakdown · `F7` swap a process back in · `F8` page a process out (`process_madvise`, Linux 5.10+) · `F2` flush every swap device (swapoff+swapon) · `F9` kill · `F3` `/` search · `F6` `s` sort · `z` show zero-swap processes · `c` full command line · `F1` help · `q` quit.
