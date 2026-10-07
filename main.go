package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
)

var version = "dev"

type view int

const (
	viewList view = iota
	viewDetail
	viewHelp
)

var sortNames = []string{"SWAP", "SWPPSS", "DELTA", "RSS", "SWAPPED", "PID", "USER", "COMMAND"}

var signals = []struct {
	name string
	sig  syscall.Signal
}{
	{"SIGTERM", syscall.SIGTERM}, {"SIGKILL", syscall.SIGKILL}, {"SIGINT", syscall.SIGINT}, {"SIGHUP", syscall.SIGHUP},
	{"SIGQUIT", syscall.SIGQUIT}, {"SIGUSR1", syscall.SIGUSR1}, {"SIGUSR2", syscall.SIGUSR2},
	{"SIGSTOP", syscall.SIGSTOP}, {"SIGCONT", syscall.SIGCONT},
}

// modal is a yes/no question (onYes), an info box (neither) or a picker (items/onPick).
type modal struct {
	title  string
	lines  []string
	onYes  func()
	items  []string
	pick   int
	onPick func(int)
}

type app struct {
	scr            tcell.Screen
	cfg            config
	sys            *Sys
	prev           map[int]uint64
	rows           []Proc
	sel, top, dtop int
	sortBy         int
	filter         string
	searching      bool
	hideZero       bool
	fullCmd        bool
	tree           bool
	view           view
	detail         Proc
	maps           []Mapping
	mapsErr        error
	modal          *modal
	busy           bool

	mu     sync.Mutex
	status string
}

var (
	stDefault = tcell.StyleDefault
	stHdr     = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorGreen)
	stSel     = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorTeal)
	stKey     = tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	stFn      = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorTeal)
	stLabel   = tcell.StyleDefault.Foreground(tcell.ColorTeal).Bold(true)
	stDim     = tcell.StyleDefault.Foreground(tcell.ColorGray)
	stWarn    = tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
	stErr     = tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true)
	stGreen   = tcell.StyleDefault.Foreground(tcell.ColorGreen)
	stYellow  = tcell.StyleDefault.Foreground(tcell.ColorYellow)
	stBlue    = tcell.StyleDefault.Foreground(tcell.ColorBlue)
	stMagenta = tcell.StyleDefault.Foreground(tcell.ColorFuchsia)
	stRed     = tcell.StyleDefault.Foreground(tcell.ColorRed)
)

func sortIndex(name string) int {
	for i, n := range sortNames {
		if n == strings.ToUpper(name) {
			return i
		}
	}
	return 0
}

func main() {
	cfg := loadConfig()
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: swaptop [-d SECONDS] [-s COLUMN] [-t] [-z] [--version]\n\nhtop-style view of swap usage per process. Press F1 inside for keys.\n\n")
		flag.PrintDefaults()
	}
	delay := flag.Float64("d", cfg.Refresh, "refresh interval in seconds")
	sortFlag := flag.String("s", cfg.Sort, "sort column: "+strings.Join(sortNames, ", "))
	tree := flag.Bool("t", cfg.Tree, "show the process tree")
	all := flag.Bool("z", !cfg.HideZero, "show processes with no swap too")
	ver := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *ver {
		fmt.Println("swaptop", version)
		return
	}
	if *delay <= 0 {
		fmt.Fprintln(os.Stderr, "swaptop: -d must be positive")
		os.Exit(2)
	}
	cfg.Refresh = *delay

	scr, err := tcell.NewScreen()
	if err == nil {
		err = scr.Init()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "swaptop:", err)
		os.Exit(1)
	}
	a := &app{scr: scr, cfg: cfg, sortBy: sortIndex(*sortFlag), tree: *tree, hideZero: !*all, fullCmd: cfg.FullCmd}
	a.sample()
	events := make(chan tcell.Event, 64)
	go func() {
		for {
			ev := scr.PollEvent()
			if ev == nil {
				return
			}
			events <- ev
		}
	}()
	tick := time.NewTicker(time.Duration(cfg.Refresh * float64(time.Second)))
loop:
	for {
		a.draw()
		select {
		case ev := <-events:
			if a.handle(ev) {
				break loop
			}
		case <-tick.C:
			a.sample()
		}
	}
	scr.Fini()
	cfg.Sort, cfg.HideZero, cfg.FullCmd, cfg.Tree = sortNames[a.sortBy], a.hideZero, a.fullCmd, a.tree
	if err := cfg.save(); err != nil {
		fmt.Fprintln(os.Stderr, "swaptop: saving config:", err)
	}
}

func (a *app) sample() {
	a.sys = collect(a.prev)
	a.prev = make(map[int]uint64, len(a.sys.Procs))
	for _, p := range a.sys.Procs {
		a.prev[p.PID] = p.Swap
	}
	a.rebuild()
	if a.view == viewDetail {
		for _, p := range a.sys.Procs {
			if p.PID == a.detail.PID {
				a.detail = p
			}
		}
		a.maps, a.mapsErr = readSmaps(a.detail.PID)
	}
}

func pct(p Proc) int {
	if p.Swap+p.RSS == 0 {
		return 0
	}
	return int(p.Swap * 100 / (p.Swap + p.RSS))
}

func (a *app) less(x, y Proc) bool {
	switch sortNames[a.sortBy] {
	case "SWPPSS":
		return x.SwapPss > y.SwapPss
	case "DELTA":
		return x.Delta > y.Delta
	case "RSS":
		return x.RSS > y.RSS
	case "SWAPPED":
		return pct(x) > pct(y)
	case "PID":
		return x.PID < y.PID
	case "USER":
		return x.User < y.User
	case "COMMAND":
		return x.Comm < y.Comm
	}
	return x.Swap > y.Swap
}

func (a *app) visible(p Proc) bool {
	if a.hideZero && p.Swap == 0 {
		return false
	}
	f := strings.ToLower(a.filter)
	return f == "" || strings.Contains(strings.ToLower(p.Cmd+" "+p.Comm+" "+p.User+" "+fmt.Sprint(p.PID)), f)
}

func (a *app) rebuild() {
	selPID := -1
	if a.sel < len(a.rows) {
		selPID = a.rows[a.sel].PID
	}
	a.rows = a.rows[:0]
	if a.tree {
		a.buildTree()
	} else {
		for _, p := range a.sys.Procs {
			if a.visible(p) {
				a.rows = append(a.rows, p)
			}
		}
		sort.SliceStable(a.rows, func(i, j int) bool { return a.less(a.rows[i], a.rows[j]) })
	}
	a.sel = 0
	for i, p := range a.rows {
		if p.PID == selPID {
			a.sel = i
		}
	}
}

// buildTree lays processes out by parent, htop style. A process is shown if it
// or any descendant passes the filter, so ancestors stay for context.
func (a *app) buildTree() {
	kids := map[int][]Proc{}
	present := map[int]bool{}
	for _, p := range a.sys.Procs {
		present[p.PID] = true
	}
	for _, p := range a.sys.Procs {
		kids[p.PPID] = append(kids[p.PPID], p)
	}
	for _, k := range kids {
		sort.SliceStable(k, func(i, j int) bool { return a.less(k[i], k[j]) })
	}
	keep := map[int]bool{}
	var mark func(p Proc) bool
	mark = func(p Proc) bool {
		ok := a.visible(p)
		for _, c := range kids[p.PID] {
			if mark(c) {
				ok = true
			}
		}
		keep[p.PID] = ok
		return ok
	}
	var walk func(p Proc, prefix string, branch string)
	walk = func(p Proc, prefix, branch string) {
		p.Tree = prefix + branch
		a.rows = append(a.rows, p)
		var shown []Proc
		for _, c := range kids[p.PID] {
			if keep[c.PID] {
				shown = append(shown, c)
			}
		}
		childPrefix := prefix
		if branch != "" {
			childPrefix += "│  "
			if branch == "└─ " {
				childPrefix = prefix + "   "
			}
		}
		for i, c := range shown {
			b := "├─ "
			if i == len(shown)-1 {
				b = "└─ "
			}
			walk(c, childPrefix, b)
		}
	}
	var roots []Proc
	for _, p := range a.sys.Procs {
		if !present[p.PPID] || p.PPID == p.PID {
			roots = append(roots, p)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool { return a.less(roots[i], roots[j]) })
	for _, r := range roots {
		if mark(r) {
			walk(r, "", "")
		}
	}
}

// ---- input ----

func (a *app) handle(ev tcell.Event) (quit bool) {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		a.scr.Sync()
	case *tcell.EventInterrupt:
		if ev.Data() == "done" {
			a.busy = false
			a.sample()
		}
	case *tcell.EventKey:
		return a.key(ev)
	}
	return false
}

func (a *app) modalKey(ev *tcell.EventKey) {
	m := a.modal
	switch {
	case m.items != nil:
		switch ev.Key() {
		case tcell.KeyUp:
			m.pick = max(m.pick-1, 0)
		case tcell.KeyDown:
			m.pick = min(m.pick+1, len(m.items)-1)
		case tcell.KeyEnter:
			a.modal = nil
			m.onPick(m.pick)
		case tcell.KeyEscape:
			a.modal = nil
		case tcell.KeyRune:
			if ev.Rune() == 'q' {
				a.modal = nil
			}
		}
	case m.onYes != nil && (ev.Rune() == 'y' || ev.Rune() == 'Y'):
		a.modal = nil
		m.onYes()
	case m.onYes == nil || ev.Key() == tcell.KeyEscape || ev.Rune() == 'n' || ev.Rune() == 'N' || ev.Rune() == 'q':
		a.modal = nil
	}
}

func (a *app) key(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyCtrlC {
		return true
	}
	if a.modal != nil {
		a.modalKey(ev)
		return false
	}
	if a.searching {
		switch ev.Key() {
		case tcell.KeyEscape:
			a.filter, a.searching = "", false
		case tcell.KeyEnter:
			a.searching = false
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if a.filter != "" {
				a.filter = a.filter[:len(a.filter)-1]
			}
		case tcell.KeyRune:
			a.filter += string(ev.Rune())
		}
		a.rebuild()
		return false
	}
	if a.view == viewHelp {
		a.view = viewList
		return false
	}
	n := len(a.rows)
	pos := &a.sel
	if a.view == viewDetail {
		n, pos = len(a.maps), &a.dtop
	}
	_, h := a.scr.Size()
	pg := max(h-10, 1)
	switch ev.Key() {
	case tcell.KeyUp:
		*pos--
	case tcell.KeyDown:
		*pos++
	case tcell.KeyPgUp:
		*pos -= pg
	case tcell.KeyPgDn:
		*pos += pg
	case tcell.KeyHome:
		*pos = 0
	case tcell.KeyEnd:
		*pos = n - 1
	case tcell.KeyEnter:
		if a.view == viewList && a.sel < len(a.rows) {
			a.detail, a.dtop = a.rows[a.sel], 0
			a.maps, a.mapsErr = readSmaps(a.detail.PID)
			a.view = viewDetail
		} else {
			a.view = viewList
		}
	case tcell.KeyEscape:
		a.view = viewList
	case tcell.KeyF1:
		a.view = viewHelp
	case tcell.KeyF2:
		a.askFlush()
	case tcell.KeyF3:
		a.searching = true
	case tcell.KeyF5:
		a.toggleTree()
	case tcell.KeyF6:
		a.cycleSort()
	case tcell.KeyF7:
		a.askSwapIn()
	case tcell.KeyF8:
		a.askSwapOut()
	case tcell.KeyF9:
		a.askKill()
	case tcell.KeyF10:
		return true
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q':
			if a.view == viewList {
				return true
			}
			a.view = viewList
		case '?', 'h':
			a.view = viewHelp
		case '/':
			a.searching = true
		case 's':
			a.cycleSort()
		case 't':
			a.toggleTree()
		case 'z':
			a.hideZero = !a.hideZero
			a.rebuild()
		case 'c':
			a.fullCmd = !a.fullCmd
		case 'i':
			a.askSwapIn()
		case 'o':
			a.askSwapOut()
		case 'k':
			a.askKill()
		case 'F':
			a.askFlush()
		}
	}
	*pos = max(0, min(*pos, n-1))
	return false
}

func (a *app) cycleSort() {
	a.sortBy = (a.sortBy + 1) % len(sortNames)
	a.rebuild()
}

func (a *app) toggleTree() {
	a.tree = !a.tree
	a.rebuild()
}

func (a *app) current() (Proc, bool) {
	if a.view == viewDetail {
		return a.detail, true
	}
	if a.sel < len(a.rows) {
		return a.rows[a.sel], true
	}
	return Proc{}, false
}

func (a *app) info(title string, lines ...string) { a.modal = &modal{title: title, lines: lines} }

func (a *app) runJob(fn func(set func(string)) error) {
	if a.busy {
		a.info("Busy", "Another action is still running.")
		return
	}
	a.busy = true
	set := func(s string) {
		a.mu.Lock()
		a.status = s
		a.mu.Unlock()
		a.scr.PostEvent(tcell.NewEventInterrupt(nil))
	}
	go func() {
		if err := fn(set); err != nil {
			set("Error: " + err.Error() + permHint(err))
		}
		a.scr.PostEvent(tcell.NewEventInterrupt("done"))
	}()
}

func (a *app) askSwapIn() {
	p, ok := a.current()
	if !ok {
		return
	}
	if p.Swap == 0 {
		a.info("Nothing to do", fmt.Sprintf("%s (%d) has no pages in swap.", p.Comm, p.PID))
		return
	}
	a.modal = &modal{title: "Swap in", lines: []string{
		fmt.Sprintf("Pull %s of %s (%d) back from swap into RAM?", human(p.Swap), p.Comm, p.PID),
		fmt.Sprintf("Available RAM: %s", human(a.sys.Mem["MemAvailable"])),
	}, onYes: func() {
		a.runJob(func(set func(string)) error {
			start, last := time.Now(), time.Time{}
			err := swapIn(p.PID, func(done, total uint64) {
				if time.Since(last) > 100*time.Millisecond {
					last = time.Now()
					set(fmt.Sprintf("Swapping in %s (%d): %s / %s", p.Comm, p.PID, human(done), human(total)))
				}
			})
			if err == nil {
				set(fmt.Sprintf("Swapped in %s (%d) in %s. Its slots stay 'cached' until the kernel drops them or you flush (F2).",
					p.Comm, p.PID, time.Since(start).Round(100*time.Millisecond)))
			}
			return err
		})
	}}
}

func (a *app) askSwapOut() {
	p, ok := a.current()
	if !ok {
		return
	}
	a.modal = &modal{title: "Swap out", lines: []string{
		fmt.Sprintf("Ask the kernel to page out %s of %s (%d) now?", human(p.RSS), p.Comm, p.PID),
		"Anonymous memory goes to swap, clean file pages are dropped.",
		"The process keeps running but will be slow until it faults back in.",
	}, onYes: func() {
		a.runJob(func(set func(string)) error {
			set(fmt.Sprintf("Swapping out %s (%d)...", p.Comm, p.PID))
			n, err := swapOut(p.PID)
			if err == nil {
				set(fmt.Sprintf("Advised kernel to page out %s of %s (%d).", human(n/1024), p.Comm, p.PID))
			}
			return err
		})
	}}
}

func (a *app) askKill() {
	p, ok := a.current()
	if !ok {
		return
	}
	items := make([]string, len(signals))
	for i, s := range signals {
		items[i] = fmt.Sprintf("%2d %s", int(s.sig), s.name)
	}
	a.modal = &modal{title: fmt.Sprintf("Send signal to %s (%d)", p.Comm, p.PID), items: items, onPick: func(i int) {
		if err := syscall.Kill(p.PID, signals[i].sig); err != nil {
			a.info("Signal failed", err.Error()+permHint(err))
			return
		}
		a.mu.Lock()
		a.status = fmt.Sprintf("Sent %s to %s (%d).", signals[i].name, p.Comm, p.PID)
		a.mu.Unlock()
	}}
}

func (a *app) askFlush() {
	used, avail := a.sys.SwapUsed(), a.sys.Mem["MemAvailable"]
	if len(a.sys.Devs) == 0 {
		a.info("No swap", "No active swap devices.")
		return
	}
	if os.Geteuid() != 0 {
		a.info("Need root", "Flushing a swap device (swapoff + swapon) needs root. Try: sudo swaptop")
		return
	}
	if used > avail {
		a.info("Not enough RAM", fmt.Sprintf("Swap holds %s but only %s RAM is available.", human(used), human(avail)),
			"Swap in / kill some processes first, or the OOM killer will decide for you.")
		return
	}
	lines := []string{fmt.Sprintf("swapoff + swapon every device, pulling %s back into RAM (%s available)?", human(used), human(avail))}
	for _, d := range a.sys.Devs {
		lines = append(lines, fmt.Sprintf("  %s  used %s  prio %d", d.Path, human(d.Used), d.Prio))
	}
	lines = append(lines, "Priority is preserved; discard/other swapon options are not.")
	devs := a.sys.Devs
	a.modal = &modal{title: "Flush swap", lines: lines, onYes: func() {
		a.runJob(func(set func(string)) error {
			start := time.Now()
			for _, d := range devs {
				set("Flushing " + d.Path + " (swapoff)...")
				if err := flushDev(d); err != nil {
					return err
				}
			}
			set("Flushed all swap devices in " + time.Since(start).Round(100*time.Millisecond).String())
			return nil
		})
	}}
}

// ---- drawing ----

func (a *app) put(x, y int, s string, st tcell.Style) int {
	w, _ := a.scr.Size()
	for _, r := range s {
		if x >= w {
			break
		}
		a.scr.SetContent(x, y, r, nil, st)
		x++
	}
	return x
}

func (a *app) fill(y int, st tcell.Style) {
	w, _ := a.scr.Size()
	for x := 0; x < w; x++ {
		a.scr.SetContent(x, y, ' ', nil, st)
	}
}

type seg struct {
	frac float64
	st   tcell.Style
}

// bar draws an htop style  Label[|||||      text]  of the given total width.
func (a *app) bar(x, y, width int, label string, segs []seg, text string) {
	x = a.put(x, y, label, stLabel)
	x = a.put(x, y, "[", stDefault)
	inner := width - len(label) - 2
	if inner < 4 {
		return
	}
	pos, filled := 0, 0
	for _, s := range segs {
		n := int(s.frac*float64(inner) + 0.5)
		n = min(n, inner-filled)
		for i := 0; i < n; i++ {
			a.scr.SetContent(x+pos, y, '|', nil, s.st)
			pos++
		}
		filled += n
	}
	if len(text) < inner {
		a.put(x+inner-len(text), y, text, stDim)
	}
	a.put(x+inner, y, "]", stDefault)
}

func (a *app) drawHeader(w int) int {
	m := a.sys.Mem
	total, used := m["SwapTotal"], a.sys.SwapUsed()
	procs := min(a.sys.ProcSwap, used)
	cached := min(m["SwapCached"], used-procs)
	other := used - procs - cached
	frac := func(v uint64) float64 {
		if total == 0 {
			return 0
		}
		return float64(v) / float64(total)
	}
	memTot := m["MemTotal"]
	memCache := m["Cached"] + m["SReclaimable"]
	memUsed := memTot - m["MemFree"] - m["Buffers"] - memCache
	mfrac := func(v uint64) float64 { return float64(v) / float64(max(memTot, 1)) }
	swapSegs := []seg{{frac(procs), stGreen}, {frac(cached), stYellow}, {frac(other), stMagenta}}
	memSegs := []seg{{mfrac(memUsed), stGreen}, {mfrac(m["Buffers"]), stBlue}, {mfrac(memCache), stYellow}}
	y := 0
	if w >= 100 {
		bw := (w - 2) / 2
		a.bar(1, y, bw, "Swp", swapSegs, human(used)+"/"+human(total))
		a.bar(1+bw+1, y, bw, "Mem", memSegs, human(memTot-m["MemAvailable"])+"/"+human(memTot))
		y++
	} else {
		a.bar(1, y, w-2, "Swp", swapSegs, human(used)+"/"+human(total))
		a.bar(1, y+1, w-2, "Mem", memSegs, human(memTot-m["MemAvailable"])+"/"+human(memTot))
		y += 2
	}
	x := a.put(1, y, "Swap used "+human(used)+":  ", stDefault)
	x = a.put(x, y, "■ "+human(procs)+" in processes   ", stGreen)
	x = a.put(x, y, "■ "+human(cached)+" cached (also in RAM, droppable)   ", stYellow)
	x = a.put(x, y, "■ "+human(other)+" tmpfs/shm/unmapped   ", stMagenta)
	a.put(x, y, "free "+human(total-used), stDim)
	y++
	if z := m["Zswapped"]; z > 0 {
		a.put(1, y, fmt.Sprintf("zswap: %s of that is held compressed in RAM (pool %s), the rest is on disk", human(z), human(m["Zswap"])), stDefault)
		y++
	}
	for _, d := range a.sys.Devs {
		p := 0
		if d.Size > 0 {
			p = int(d.Used * 100 / d.Size)
		}
		st := stDefault
		if p >= 90 {
			st = stRed
		}
		x := a.put(1, y, fmt.Sprintf("%-28s %-9s %8s  used %s (%d%%)  prio %d", d.Path, d.Type, human(d.Size), human(d.Used), p, d.Prio), st)
		if d.Zram != "" {
			a.put(x+2, y, d.Zram, stDim)
		}
		y++
	}
	line := fmt.Sprintf("Commit %s / %s   %d of %d processes in swap   sort: %s", human(m["Committed_AS"]), human(m["CommitLimit"]),
		a.sys.Swapped, len(a.sys.Procs), sortNames[a.sortBy])
	if a.tree {
		line += "   tree"
	}
	if a.hideZero {
		line += "   (z: show all)"
	}
	if a.filter != "" {
		line += "   filter: " + a.filter
	}
	x = a.put(1, y, line, stDefault)
	if a.sys.Unreadable > 0 {
		a.put(x+3, y, fmt.Sprintf("%d processes unreadable (not root)", a.sys.Unreadable), stWarn)
	}
	y++
	a.mu.Lock()
	status := a.status
	a.mu.Unlock()
	st := stWarn
	if strings.HasPrefix(status, "Error") {
		st = stErr
	}
	a.put(1, y, status, st)
	return y + 2
}

func (a *app) draw() {
	a.scr.Clear()
	w, h := a.scr.Size()
	switch a.view {
	case viewHelp:
		a.drawHelp()
	case viewDetail:
		a.drawDetail(w, h)
	default:
		a.drawList(w, h)
	}
	a.drawBottom(w, h)
	if a.modal != nil {
		a.drawModal(w, h)
	}
	a.scr.Show()
}

func (a *app) drawList(w, h int) {
	y := a.drawHeader(w)
	a.fill(y, stHdr)
	a.put(0, y, fmt.Sprintf("%7s %-10s %8s %8s %7s %8s %-13s %s", "PID", "USER", "SWAP", "SWPPSS", "DELTA", "RSS", "SWAPPED", "COMMAND"), stHdr)
	y++
	avail := h - 1 - y
	if avail < 1 {
		return
	}
	if a.sel < a.top {
		a.top = a.sel
	}
	if a.sel >= a.top+avail {
		a.top = a.sel - avail + 1
	}
	for i := a.top; i < len(a.rows) && i < a.top+avail; i++ {
		p := a.rows[i]
		st, bst, dst := stDefault, stGreen, stDefault
		if i == a.sel {
			a.fill(y, stSel)
			st, bst, dst = stSel, stSel, stSel
		} else {
			if pct(p) >= 75 {
				bst = stRed
			} else if pct(p) >= 25 {
				bst = stYellow
			}
			if p.Delta > 0 {
				dst = stRed
			} else if p.Delta < 0 {
				dst = stGreen
			}
		}
		pss := "-"
		if p.HasPss {
			pss = human(p.SwapPss)
		} else if p.Swap == 0 {
			pss = "0K"
		}
		x := a.put(0, y, fmt.Sprintf("%7d %-10.10s %8s %8s ", p.PID, p.User, human(p.Swap), pss), st)
		x = a.put(x, y, fmt.Sprintf("%7s", delta(p.Delta)), dst)
		x = a.put(x, y, fmt.Sprintf(" %8s ", human(p.RSS)), st)
		n := pct(p) * 8 / 100
		x = a.put(x, y, strings.Repeat("|", n), bst)
		x = a.put(x, y, strings.Repeat(" ", 8-n)+fmt.Sprintf("%3d%% ", pct(p)), st)
		cmd := p.Comm
		if a.fullCmd {
			cmd = p.Cmd
		}
		tst := stDim
		if i == a.sel {
			tst = stSel
		}
		x = a.put(x, y, p.Tree, tst)
		if a.tree && p.Swap == 0 && i != a.sel {
			st = stDim
		}
		a.put(x, y, cmd, st)
		y++
	}
}

func (a *app) drawDetail(w, h int) {
	p := a.detail
	a.put(1, 0, fmt.Sprintf("%s (%d)  user %s", p.Comm, p.PID, p.User), stLabel)
	a.put(1, 1, fmt.Sprintf("Swap %s   SwapPss %s   RSS %s   %d%% of its memory is in swap   %d mappings",
		human(p.Swap), human(p.SwapPss), human(p.RSS), pct(p), len(a.maps)), stDefault)
	a.put(1, 2, p.Cmd, stDim)
	if a.mapsErr != nil {
		a.put(1, 3, "cannot read mappings: "+a.mapsErr.Error()+permHint(a.mapsErr), stErr)
	}
	y := 4
	a.fill(y, stHdr)
	a.put(0, y, fmt.Sprintf("%8s %8s %8s %-5s %-33s %s", "SWAP", "SWPPSS", "RSS", "PERM", "ADDRESS", "MAPPING"), stHdr)
	y++
	avail := h - 1 - y
	if avail < 1 {
		return
	}
	a.dtop = max(0, min(a.dtop, len(a.maps)-avail))
	for i := a.dtop; i < len(a.maps) && i < a.dtop+avail; i++ {
		m := a.maps[i]
		st := stDefault
		if m.Swap == 0 {
			st = stDim
		}
		path := m.Path
		if path == "" {
			path = "[anon]"
		}
		a.put(0, y, fmt.Sprintf("%8s %8s %8s %-5s %012x-%012x %s", human(m.Swap), human(m.SwapPss), human(m.Rss), m.Perms, m.Start, m.End, path), st)
		y++
	}
}

func (a *app) drawHelp() {
	lines := []string{
		"swaptop " + version + " - who is using swap, and how",
		"",
		"Columns",
		"  SWAP      swapped memory this process maps: its anonymous pages plus any shared memory (shmem/tmpfs) it has mapped",
		"  SWPPSS    its own share of anonymous swap (pages shared with other processes are split between them); shared memory is not counted",
		"  DELTA     change in SWAP since the last refresh (red = being swapped out, green = coming back in)",
		"  RSS       resident memory, in RAM",
		"  SWAPPED   how much of the process (SWAP+RSS) is in swap right now",
		"",
		"Swap bar",
		"  green     attributed to processes",
		"  yellow    swap cache: pages that are in RAM *and* still hold a swap slot (reserved, not really swapped out; dropped instantly under pressure)",
		"  magenta   not owned by any process: tmpfs/shm files, or slots whose owner we cannot read",
		"",
		"Actions (each asks for confirmation)",
		"  F7 i      swap in:  fault every swapped page of the process back into RAM (reads /proc/PID/mem)",
		"  F8 o      swap out: ask the kernel to reclaim the process's pages now (process_madvise MADV_PAGEOUT, Linux 5.10+)",
		"  F2 F      flush:    swapoff + swapon every device, emptying swap completely (root, needs enough free RAM)",
		"  F9 k      send a signal (picker)",
		"",
		"Keys",
		"  Up/Down PgUp/PgDn Home/End  move          Enter  per-mapping breakdown of the selected process",
		"  F3 /  search     F6 s  sort     F5 t  tree view     z  show/hide processes with no swap     c  full command line     F10 q  quit",
		"",
		"Settings (sort, tree, z, c, refresh) are saved to " + configPath() + " on exit.",
		"Non-root: SWPPSS and actions only work for your own processes (kernel.yama.ptrace_scope).",
		"",
		"Press any key to return.",
	}
	for i, l := range lines {
		st := stDefault
		if i == 0 || (l != "" && l[0] != ' ') {
			st = stLabel
		}
		a.put(1, i, l, st)
	}
}

func (a *app) drawBottom(w, h int) {
	y := h - 1
	a.fill(y, stDefault)
	if a.searching {
		a.put(0, y, "Search: "+a.filter+"_", stWarn)
		return
	}
	keys := [][2]string{{"F1", "Help"}, {"F2", "Flush"}, {"F3", "Search"}, {"F5", "Tree"}, {"F6", "SortBy"}, {"F7", "SwapIn"}, {"F8", "SwapOut"}, {"F9", "Kill"}, {"F10", "Quit"}}
	if a.view != viewList {
		keys = [][2]string{{"Esc", "Back"}, {"F7", "SwapIn"}, {"F8", "SwapOut"}, {"F9", "Kill"}, {"F10", "Quit"}}
	}
	x := 0
	for _, k := range keys {
		x = a.put(x, y, fmt.Sprintf("%3s", k[0]), stKey)
		x = a.put(x, y, fmt.Sprintf("%-7s", k[1]), stFn)
	}
}

func (a *app) drawModal(w, h int) {
	m := a.modal
	lines := append([]string{}, m.lines...)
	switch {
	case m.items != nil:
		lines = append(lines, m.items...)
		lines = append(lines, "", "[Enter] send    [Esc] cancel")
	case m.onYes != nil:
		lines = append(lines, "", "[y] yes    [n] no")
	default:
		lines = append(lines, "", "[Enter] ok")
	}
	bw := len(m.title)
	for _, l := range lines {
		bw = max(bw, len(l))
	}
	bw = min(bw+4, w)
	bh := len(lines) + 2
	x0, y0 := (w-bw)/2, (h-bh)/2
	st := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorSilver)
	for y := y0; y < y0+bh; y++ {
		for x := x0; x < x0+bw; x++ {
			a.scr.SetContent(x, y, ' ', nil, st)
		}
	}
	a.put(x0+2, y0, m.title, st.Bold(true))
	for i, l := range lines {
		ls := st
		if m.items != nil && i >= len(m.lines) && i-len(m.lines) == m.pick {
			ls = stSel
			for x := x0 + 1; x < x0+bw-1; x++ {
				a.scr.SetContent(x, y0+1+i, ' ', nil, ls)
			}
		}
		a.put(x0+2, y0+1+i, l, ls)
	}
}

func human(kb uint64) string {
	switch {
	case kb < 1024:
		return fmt.Sprintf("%dK", kb)
	case kb < 1024*1024:
		return fmt.Sprintf("%.1fM", float64(kb)/1024)
	default:
		return fmt.Sprintf("%.2fG", float64(kb)/1024/1024)
	}
}

func delta(kb int64) string {
	switch {
	case kb > 0:
		return "+" + human(uint64(kb))
	case kb < 0:
		return "-" + human(uint64(-kb))
	}
	return ""
}
