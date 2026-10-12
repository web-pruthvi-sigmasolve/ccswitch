package identity

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const (
	maxScrapeFile = 50 << 20 // cap per IndexedDB file read
	maxLogTail    = 16 << 20 // how much of the end of main.log is searched
	maxConfigRead = 8 << 20
)

var (
	uuidIn  = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	emailIn = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

// readLastKnownAccount returns lastKnownAccountUuid from Claude Desktop's flat
// config.json. Every other key (including oauth:* secrets) is decoded away
// immediately and never retained.
func readLastKnownAccount(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var c struct {
		Last string `json:"lastKnownAccountUuid"`
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigRead))
	if err != nil {
		return ""
	}
	if json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &c) != nil {
		return ""
	}
	if !uuidIn.MatchString(c.Last) {
		return ""
	}
	return c.Last
}

// readLastIdentity finds the last "[account] Identity changed" line in the first
// readable log among paths and returns the last UUID on it.
func readLastIdentity(paths []string) string {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		off := int64(0)
		if fi.Size() > maxLogTail {
			off = fi.Size() - maxLogTail
		}
		buf := make([]byte, fi.Size()-off)
		n, _ := f.ReadAt(buf, off)
		f.Close()
		buf = buf[:n]
		marker := []byte("[account] Identity changed")
		i := bytes.LastIndex(buf, marker)
		if i < 0 {
			return ""
		}
		line := buf[i:]
		if nl := bytes.IndexByte(line, '\n'); nl >= 0 {
			line = line[:nl]
		}
		ids := uuidIn.FindAll(line, -1)
		if len(ids) == 0 {
			return ""
		}
		return string(ids[len(ids)-1])
	}
	return ""
}

// scrapeProfiles walks the IndexedDB folder looking for account UUIDs that sit
// just before an email_address field. Best effort: any error is ignored.
func scrapeProfiles(root string) map[string]profile {
	out := map[string]profile{}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return out
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		data, _ := io.ReadAll(io.LimitReader(f, maxScrapeFile))
		f.Close()
		if !bytes.Contains(data, []byte("email_address")) {
			return nil
		}
		scanProfiles(string(data), out)
		return nil
	})
	return out
}

// scanProfiles ports the legacy script's scan: for each UUID, look at a window
// starting up to 500 bytes before it and up to 1800 bytes long.
func scanProfiles(text string, out map[string]profile) {
	for _, loc := range uuidIn.FindAllStringIndex(text, -1) {
		ws := loc[0] - 500
		if ws < 0 {
			ws = 0
		}
		we := ws + 1800
		if we > len(text) {
			we = len(text)
		}
		window := text[ws:we]
		if !strings.Contains(window, "email_address") {
			continue
		}
		at := loc[0] - ws
		raw, ok := quotedField(window, at, "email_address")
		if !ok {
			continue
		}
		email := emailIn.FindString(raw)
		if email == "" {
			continue
		}
		full, _ := quotedField(window, at, "full_name")
		disp, _ := quotedField(window, at, "display_name")
		name := disp
		if full != "" && full != disp {
			name = full
		}
		out[text[loc[0]:loc[1]]] = profile{name: name, email: email}
	}
}

// quotedField finds field at or after start and returns the next quoted string
// value containing an alphanumeric character, after trimming leading characters
// that are not word characters or '@'.
func quotedField(text string, start int, field string) (string, bool) {
	fi := strings.Index(text[start:], field)
	if fi < 0 {
		return "", false
	}
	pos := start + fi + len(field)
	for {
		q := strings.IndexByte(text[pos:], '"')
		if q < 0 || pos+q >= len(text)-1 {
			return "", false
		}
		vs := pos + q + 1
		ve := strings.IndexByte(text[vs:], '"')
		if ve < 0 {
			return "", false
		}
		v := strings.TrimSpace(text[vs : vs+ve])
		v = strings.TrimSpace(strings.TrimLeftFunc(v, func(r rune) bool {
			return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '@')
		}))
		if strings.ContainsAny(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") {
			return v, true
		}
		pos = vs + ve + 1
	}
}
