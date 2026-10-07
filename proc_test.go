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
