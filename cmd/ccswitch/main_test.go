package main

import "testing"

func TestParseTUIArgs(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, true},
		{[]string{"--ascii"}, true},
		{[]string{"--no-color", "--ascii"}, true},
		{[]string{"--user-data", "/x"}, true},
		{[]string{"--user-data=/x", "--config-dir", "/y"}, true},
		{[]string{"-h"}, false},
		{[]string{"--help"}, false},
		{[]string{"-v"}, false},
		{[]string{"--version"}, false},
		{[]string{"list"}, false},
		{[]string{"--ascii", "list"}, false},
		{[]string{"--json"}, false},
		{[]string{"--quiet"}, false},
		{[]string{"--user-data"}, false},
	}
	for _, c := range cases {
		if _, got := parseTUIArgs(c.args); got != c.want {
			t.Errorf("parseTUIArgs(%q) = %v, want %v", c.args, got, c.want)
		}
	}
	f, _ := parseTUIArgs([]string{"--user-data", "/x", "--config-dir=/y", "--ascii", "--no-color"})
	if f.userData != "/x" || f.configDir != "/y" || !f.ascii || !f.noColor {
		t.Errorf("flags: %+v", f)
	}
}
