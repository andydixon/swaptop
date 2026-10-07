package main

import (
	"bytes"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
)

// All sizes are in kB, matching /proc.

type SwapDev struct {
	Path, Type string
	Size, Used uint64
	Prio       int
	Zram       string // "orig→compressed" for zram devices, else ""
}

type Proc struct {
	PID, PPID, UID     int
	Tree               string // tree-view prefix, display only
	User, Comm, Cmd    string
	Swap, SwapPss, RSS uint64
	Delta              int64 // change in Swap since previous sample
	HasPss             bool  // smaps_rollup was readable
}

type Mapping struct {
	Start, End         uint64
	Perms, Path, Flags string
	Rss, Swap, SwapPss uint64
}

type Sys struct {
	Mem        map[string]uint64 // /proc/meminfo
	Devs       []SwapDev
	Procs      []Proc
	ProcSwap   uint64 // sum of per-process SwapPss (Swap where PSS unreadable)
	Swapped    int    // processes with Swap > 0
	Unreadable int    // swapped processes whose smaps_rollup we could not read
}

func (s *Sys) SwapUsed() uint64 { return s.Mem["SwapTotal"] - s.Mem["SwapFree"] }

func collect(prev map[int]uint64) *Sys {
	s := &Sys{Mem: parseKV(readFile("/proc/meminfo")), Devs: readSwaps()}
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p, ok := readProc(pid)
		if !ok {
			continue
		}
		if pv, ok := prev[pid]; ok {
			p.Delta = int64(p.Swap) - int64(pv)
		}
		if p.Swap > 0 {
			s.Swapped++
			if p.HasPss {
				s.ProcSwap += p.SwapPss
			} else {
				s.ProcSwap += p.Swap
				s.Unreadable++
			}
		}
		s.Procs = append(s.Procs, p)
	}
	return s
}

func readProc(pid int) (Proc, bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	st := readFile(dir + "/status")
	if st == nil {
		return Proc{}, false
	}
	p := Proc{PID: pid}
	for _, line := range strings.Split(string(st), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Name":
			p.Comm = v
		case "PPid":
			p.PPID, _ = strconv.Atoi(v)
		case "Uid":
			p.UID, _ = strconv.Atoi(strings.Fields(v)[0])
		case "VmRSS":
			p.RSS = kb(v)
		case "VmSwap":
			p.Swap = kb(v)
		}
	}
	p.User = userName(p.UID)
	cmd := bytes.TrimRight(readFile(dir+"/cmdline"), "\x00")
	p.Cmd = string(bytes.ReplaceAll(cmd, []byte{0}, []byte{' '}))
	if p.Cmd == "" {
		p.Cmd = "[" + p.Comm + "]"
	}
	if p.Swap > 0 {
		if r := readFile(dir + "/smaps_rollup"); r != nil {
			m := parseKV(r) // rollup Swap also counts swapped shmem this process maps; VmSwap is anon only
			p.Swap, p.SwapPss, p.HasPss = m["Swap"], m["SwapPss"], true
		}
	}
	return p, true
}

func readSmaps(pid int) ([]Mapping, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps")
	if err != nil {
		return nil, err
	}
	return parseSmaps(b), nil
}

func parseSmaps(b []byte) []Mapping {
	var out []Mapping
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if !strings.HasSuffix(f[0], ":") { // VMA header: start-end perms offset dev inode [path]
			lo, hi, ok := strings.Cut(f[0], "-")
			if !ok || len(f) < 5 {
				continue
			}
			m := Mapping{Perms: f[1]}
			m.Start, _ = strconv.ParseUint(lo, 16, 64)
			m.End, _ = strconv.ParseUint(hi, 16, 64)
			if len(f) > 5 {
				m.Path = strings.Join(f[5:], " ")
			}
			out = append(out, m)
			continue
		}
		if len(out) == 0 {
			continue
		}
		m := &out[len(out)-1]
		switch f[0] {
		case "Rss:":
			m.Rss = kb(f[1])
		case "Swap:":
			m.Swap = kb(f[1])
		case "SwapPss:":
			m.SwapPss = kb(f[1])
		case "VmFlags:":
			m.Flags = strings.Join(f[1:], " ")
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Swap > out[j].Swap })
	return out
}

func readSwaps() []SwapDev {
	var devs []SwapDev
	for i, line := range strings.Split(string(readFile("/proc/swaps")), "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 5 {
			continue
		}
		d := SwapDev{Path: f[0], Type: f[1], Size: kb(f[2]), Used: kb(f[3])}
		d.Prio, _ = strconv.Atoi(f[4])
		if strings.HasPrefix(d.Path, "/dev/zram") {
			// mm_stat: orig_data_size compr_data_size mem_used_total ... (bytes)
			if f := strings.Fields(string(readFile("/sys/block/" + strings.TrimPrefix(d.Path, "/dev/") + "/mm_stat"))); len(f) >= 3 {
				o, _ := strconv.ParseUint(f[0], 10, 64)
				c, _ := strconv.ParseUint(f[2], 10, 64)
				d.Zram = human(o/1024) + "→" + human(c/1024) + " RAM"
			}
		}
		devs = append(devs, d)
	}
	return devs
}

// parseKV parses "Key:  123 kB" style files (meminfo, smaps_rollup) into kB values.
func parseKV(b []byte) map[string]uint64 {
	m := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			m[k] = kb(v)
		}
	}
	return m
}

func kb(v string) uint64 {
	f := strings.Fields(v)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.ParseUint(f[0], 10, 64)
	return n
}

func readFile(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

var users = map[int]string{}

func userName(uid int) string {
	if n, ok := users[uid]; ok {
		return n
	}
	n := strconv.Itoa(uid)
	if u, err := user.LookupId(n); err == nil {
		n = u.Username
	}
	users[uid] = n
	return n
}
