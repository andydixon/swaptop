package main

import "testing"

func TestParseSmaps(t *testing.T) {
	in := []byte(`7f00-7f10 rw-p 00000000 00:00 0                          [heap]
Rss:                 100 kB
Swap:                 40 kB
SwapPss:              20 kB
VmFlags: rd wr mr mw me ac
7f20-7f30 r--p 00000000 08:01 1234 /usr/lib/x.so (deleted)
Rss:                   8 kB
Swap:                  0 kB
`)
	m := parseSmaps(in)
	if len(m) != 2 || m[0].Path != "[heap]" || m[0].Swap != 40 || m[0].SwapPss != 20 || m[0].Rss != 100 ||
		m[0].Start != 0x7f00 || m[0].End != 0x7f10 || m[0].Flags != "rd wr mr mw me ac" || m[1].Path != "/usr/lib/x.so (deleted)" {
		t.Fatalf("bad parse: %+v", m)
	}
	if parseKV([]byte("SwapTotal:  16 kB\nFoo: 3\n"))["SwapTotal"] != 16 {
		t.Fatal("parseKV")
	}
}

func TestTree(t *testing.T) {
	a := &app{hideZero: true, sys: &Sys{Procs: []Proc{
		{PID: 1, PPID: 0, Comm: "init"},
		{PID: 2, PPID: 1, Comm: "a", Swap: 10},
		{PID: 3, PPID: 2, Comm: "b", Swap: 5},
		{PID: 4, PPID: 1, Comm: "c"}, // no swap anywhere below: hidden
	}}, tree: true}
	a.rebuild()
	if len(a.rows) != 3 || a.rows[0].PID != 1 || a.rows[1].Tree != "└─ " || a.rows[2].Tree != "   └─ " {
		t.Fatalf("bad tree: %+v", a.rows)
	}
}

func TestConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := defaults
	c.Sort, c.Tree, c.Refresh = "RSS", true, 0.5
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(); got != c {
		t.Fatalf("round trip: %+v != %+v", got, c)
	}
}
