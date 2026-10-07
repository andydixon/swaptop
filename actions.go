package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	sysPidfdOpen      = 434 // same number on every arch
	sysProcessMadvise = 440
	madvPageout       = 21
	swapFlagPrefer    = 0x8000
	swapFlagPrioMask  = 0x7fff
)

// swapIn faults every swapped-out page of pid back into RAM by reading it
// through /proc/pid/mem. Pages are located via /proc/pid/pagemap so only
// swapped pages are touched. Needs ptrace access to the target.
func swapIn(pid int, progress func(doneKB, totalKB uint64)) error {
	maps, err := readSmaps(pid)
	if err != nil {
		return err
	}
	dir := "/proc/" + strconv.Itoa(pid)
	mem, err := os.Open(dir + "/mem")
	if err != nil {
		return err
	}
	defer mem.Close()
	pm, err := os.Open(dir + "/pagemap")
	if err != nil {
		return err
	}
	defer pm.Close()

	page := uint64(os.Getpagesize())
	var total, done uint64
	for _, m := range maps {
		total += m.Swap
	}
	const chunk = 4096 // pagemap entries per read
	ents := make([]byte, chunk*8)
	buf := make([]byte, 256*page)
	swapped := func(i int) bool {
		e := binary.LittleEndian.Uint64(ents[i*8:])
		return e&(1<<62) != 0 && e&(1<<63) == 0 // in swap, not present
	}
	for _, m := range maps {
		if m.Swap == 0 {
			continue
		}
		if shmem(m) {
			// swapped shmem pages leave no swap PTE behind (the entry lives in the
			// page cache), so pagemap can't find them: just read the whole range.
			for addr := m.Start; addr < m.End; addr += uint64(len(buf)) {
				n := min(m.End-addr, uint64(len(buf)))
				mem.ReadAt(buf[:n], int64(addr))
				done += n / 1024
				progress(min(done, total), total)
			}
			continue
		}
		for addr := m.Start; addr < m.End; addr += chunk * page {
			n := min((m.End-addr)/page, chunk)
			got, err := pm.ReadAt(ents[:n*8], int64(addr/page*8))
			if got == 0 && err != nil {
				break
			}
			n = uint64(got / 8)
			for i := 0; i < int(n); {
				if !swapped(i) {
					i++
					continue
				}
				j := i + 1
				for j < int(n) && j-i < 256 && swapped(j) {
					j++
				}
				mem.ReadAt(buf[:uint64(j-i)*page], int64(addr+uint64(i)*page)) // errors (guard pages etc.) are fine
				done += uint64(j-i) * page / 1024
				progress(done, total)
				i = j
			}
		}
	}
	return nil
}

// swapOut asks the kernel to reclaim pid's resident pages right now
// (process_madvise MADV_PAGEOUT, Linux 5.10+). Anonymous pages go to swap,
// clean file-backed pages are simply dropped. Returns bytes advised.
func swapOut(pid int) (uint64, error) {
	maps, err := readSmaps(pid)
	if err != nil {
		return 0, err
	}
	fd, _, e := syscall.Syscall(sysPidfdOpen, uintptr(pid), 0, 0)
	if e != 0 {
		return 0, fmt.Errorf("pidfd_open: %w", e)
	}
	defer syscall.Close(int(fd))
	var advised uint64
	var lastErr error
	for _, m := range maps {
		if m.Rss == 0 || skipPageout(m) {
			continue
		}
		iov := struct{ base, len uintptr }{uintptr(m.Start), uintptr(m.End - m.Start)}
		n, _, e := syscall.Syscall6(sysProcessMadvise, fd, uintptr(unsafe.Pointer(&iov)), 1, madvPageout, 0, 0)
		if e != 0 {
			lastErr = e
			continue
		}
		advised += uint64(n)
	}
	if advised == 0 && lastErr != nil {
		if errors.Is(lastErr, syscall.ENOSYS) {
			return 0, errors.New("process_madvise unsupported (needs Linux 5.10+)")
		}
		return 0, fmt.Errorf("process_madvise: %w", lastErr)
	}
	return advised, nil
}

// ponytail: heuristic; a MAP_PRIVATE mapping of a tmpfs file is missed.
func shmem(m Mapping) bool {
	if len(m.Perms) > 3 && m.Perms[3] == 's' {
		return true
	}
	for _, p := range []string{"/dev/zero", "/dev/shm/", "/memfd:", "/SYSV"} {
		if strings.HasPrefix(m.Path, p) {
			return true
		}
	}
	return false
}

func skipPageout(m Mapping) bool {
	switch m.Path {
	case "[vsyscall]", "[vvar]", "[vdso]":
		return true
	}
	for _, f := range []string{" lo", " pf", " io", " ht"} { // locked, pfnmap, io, hugetlb
		if strings.Contains(" "+m.Flags+" ", f+" ") {
			return true
		}
	}
	return false
}

// flushDev empties a swap device by swapoff+swapon, keeping its priority.
// Needs root and enough free RAM to hold everything currently on the device.
func flushDev(d SwapDev) error {
	p, err := syscall.BytePtrFromString(d.Path)
	if err != nil {
		return err
	}
	if _, _, e := syscall.Syscall(syscall.SYS_SWAPOFF, uintptr(unsafe.Pointer(p)), 0, 0); e != 0 {
		return fmt.Errorf("swapoff %s: %w", d.Path, e)
	}
	flags := uintptr(0)
	if d.Prio >= 0 {
		flags = swapFlagPrefer | uintptr(d.Prio&swapFlagPrioMask)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_SWAPON, uintptr(unsafe.Pointer(p)), flags, 0); e != 0 {
		return fmt.Errorf("swapon %s: %w (device is now OFF - re-enable manually)", d.Path, e)
	}
	return nil
}

func permHint(err error) string {
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		return " (try: sudo swaptop)"
	}
	return ""
}
