package merge

import (
	"reflect"
	"strings"
	"testing"
)

func TestMergeTable(t *testing.T) {
	p := DefaultPolicy()
	tests := []struct {
		name, src, dst, want string
	}{
		{
			name: "source owned copied, dest order preserved",
			src:  `{"title":"new","sessionId":"x","lastActivityAt":5}`,
			dst:  `{"sessionId":"x","lastActivityAt":1,"title":"old"}`,
			want: `{"sessionId":"x","lastActivityAt":5,"title":"new"}`,
		},
		{
			name: "unknown dest keys survive",
			src:  `{"title":"n"}`,
			dst:  `{"mystery":{"a":[1,2]},"title":"o"}`,
			want: `{"mystery":{"a":[1,2]},"title":"n"}`,
		},
		{
			name: "src-only unknown key appended in src order",
			src:  `{"zeta":1,"alpha":2,"title":"n"}`,
			dst:  `{"title":"o"}`,
			want: `{"title":"n","zeta":1,"alpha":2}`,
		},
		{
			name: "dest owned never overwritten nor added",
			src:  `{"lastFocusedAt":9,"remoteMcpServersConfig":[1],"terminalClaudeTabOrdinal":3}`,
			dst:  `{"lastFocusedAt":1,"remoteMcpServersConfig":[]}`,
			want: `{"lastFocusedAt":1,"remoteMcpServersConfig":[]}`,
		},
		{
			name: "BOM and CRLF inputs",
			src:  "\xEF\xBB\xBF{\r\n  \"title\": \"n\"\r\n}\r\n",
			dst:  "\xEF\xBB\xBF{\r\n  \"title\": \"o\",\r\n  \"x\": 1\r\n}\r\n",
			want: `{"title":"n","x":1}`,
		},
		{
			name: "html chars not escaped",
			src:  `{"title":"a<b>&c"}`,
			dst:  `{"title":"x","note":"<&>"}`,
			want: `{"title":"a<b>&c","note":"<&>"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Merge([]byte(tc.src), []byte(tc.dst), p)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
			if strings.HasSuffix(string(got), "\n") {
				t.Fatal("trailing newline")
			}
		})
	}
}

func TestMergeRejects(t *testing.T) {
	for _, bad := range []string{`[]`, `"s"`, `1`, ``, `{"a":`, `{"a":1} x`, `null`} {
		if _, err := Merge([]byte(bad), []byte(`{}`), DefaultPolicy()); err == nil {
			t.Errorf("src %q accepted", bad)
		}
		if _, err := Merge([]byte(`{}`), []byte(bad), DefaultPolicy()); err == nil {
			t.Errorf("dst %q accepted", bad)
		}
	}
}

func TestPrepareAdd(t *testing.T) {
	p := DefaultPolicy()
	src := "\xEF\xBB\xBF{\"b\": 1, \"remoteMcpServersConfig\": [{\"x\":1}], \"a\": \"<\"}\n"
	got, err := PrepareAdd([]byte(src), p, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"b":1,"remoteMcpServersConfig":[],"a":"<"}`; string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
	got, err = PrepareAdd([]byte(src), p, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.TrimPrefix(src, "\xEF\xBB\xBF") {
		t.Fatalf("unstripped bytes changed: %q", got)
	}
	got, err = PrepareAdd([]byte(`{"a":1}`), p, true)
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("absent key must not be added: %s %v", got, err)
	}
	if _, err := PrepareAdd([]byte(`[1]`), p, true); err == nil {
		t.Fatal("non-object accepted")
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{`{"a":1,"b":[1,2]}`, "{ \"b\" : [1,2],\r\n\"a\":1 }", true},
		{`{"a":1}`, `{"a":2}`, false},
		{`{"a":[1,2]}`, `{"a":[2,1]}`, false},
		{`{"a":1}`, `{"a":1,"b":null}`, false},
		{`{`, `{}`, false},
		{"\xEF\xBB\xBF{}", `{}`, true},
	}
	for _, tc := range tests {
		if got := Equal([]byte(tc.a), []byte(tc.b)); got != tc.want {
			t.Errorf("Equal(%q,%q)=%v", tc.a, tc.b, got)
		}
	}
}

func TestDiffKeys(t *testing.T) {
	got := DiffKeys(
		[]byte(`{"a":1,"b":2,"c":3,"onlyA":1}`),
		[]byte(`{"c":3,"b":9,"a":1,"onlyB":true}`),
	)
	want := []string{"b", "onlyA", "onlyB"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := DiffKeys([]byte(`{"a":1}`), []byte(`{"a":1}`)); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if DiffKeys([]byte(`[`), []byte(`{}`)) != nil {
		t.Fatal("invalid input should give nil")
	}
}
