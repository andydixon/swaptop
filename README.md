# swaptop

htop-style view of which processes are using swap, and how. Linux only.

![swaptop](https://img.shields.io/badge/platform-linux-blue)

The header splits swap in use into memory owned by processes (green), swap
cache (yellow: pages that are in RAM *and* still hold a swap slot, so
reserved rather than really swapped out) and tmpfs/shm (magenta). zswap and
zram figures appear when present. Each process shows its SWAP, proportional
SWPPSS, DELTA since the last refresh, RSS and how much of it is in swap;
`Enter` breaks a process down by mapping (heap, stacks, libraries, files).

Actions, each confirmed first: swap a process back in (`F7`), page a process
out (`F8`, `process_madvise`, Linux 5.10+), flush every swap device with
swapoff+swapon (`F2`), send a signal (`F9`). Search (`/`), sort (`F6`), tree
view (`F5`), zero-swap toggle (`z`), full command line (`c`). Settings
persist in `~/.config/swaptop/config`. See `man swaptop`.

Run it as root to see every process and use the actions:

```sh
sudo swaptop
```

## Install

```sh
# Homebrew (Linux)
brew install andydixon/tap/swaptop

# Debian, Ubuntu
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://repo.dixon.cx/dixon.gpg | sudo tee /etc/apt/keyrings/dixon.gpg >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/dixon.gpg] https://repo.dixon.cx/apt stable main" |
    sudo tee /etc/apt/sources.list.d/dixon.list
sudo apt update && sudo apt install swaptop

# Fedora, RHEL, AlmaLinux, Rocky
sudo curl -fsSL -o /etc/yum.repos.d/dixon.repo https://repo.dixon.cx/rpm/dixon.repo
sudo dnf install swaptop

# openSUSE
sudo zypper addrepo https://repo.dixon.cx/rpm/dixon.repo
sudo zypper install swaptop

# Arch Linux
curl -fsSL https://repo.dixon.cx/dixon.asc | sudo pacman-key --add -
sudo pacman-key --lsign-key C22FF7330668417C62C7A304CBC7951D7F2234D1
printf '\n[dixon]\nServer = https://repo.dixon.cx/arch/$arch\n' | sudo tee -a /etc/pacman.conf
sudo pacman -Sy swaptop

# From source
go install github.com/andydixon/swaptop@latest
# or
git clone https://github.com/andydixon/swaptop && cd swaptop && make && sudo make install
```

More at [repo.dixon.cx](https://repo.dixon.cx).

## Licence

GPL-3.0-or-later. Andy Dixon, [www.dixon.cx](https://www.dixon.cx).
