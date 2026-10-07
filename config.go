package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// config is ~/.config/swaptop/config: "key = value" lines, rewritten on exit.
type config struct {
	Sort     string
	HideZero bool
	FullCmd  bool
	Tree     bool
	Refresh  float64 // seconds
}

var defaults = config{Sort: "SWAP", HideZero: true, Refresh: 2}

func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "swaptop", "config")
}

func loadConfig() config {
	c := defaults
	b, err := os.ReadFile(configPath())
	if err != nil {
		return c
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "sort":
			c.Sort = strings.ToUpper(v)
		case "hide_zero":
			c.HideZero = v == "true"
		case "full_cmd":
			c.FullCmd = v == "true"
		case "tree":
			c.Tree = v == "true"
		case "refresh":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				c.Refresh = f
			}
		}
	}
	return c
}

func (c config) save() error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(fmt.Sprintf(
		"# swaptop configuration; rewritten when swaptop exits. See swaptop(1).\nsort = %s\nhide_zero = %t\nfull_cmd = %t\ntree = %t\nrefresh = %g\n",
		c.Sort, c.HideZero, c.FullCmd, c.Tree, c.Refresh)), 0o644)
}
