// Package chunkmap extracts a Webpack chunk inventory, not a route table.
package chunkmap

import (
	"net/url"
	"regexp"
	"strings"
)

type Inventory struct {
	RuntimeURL string
	PublicPath string
	Chunks     map[string]string // chunk ID -> resolved URL; not fetched or verified
}

var resolver = regexp.MustCompile(`([A-Za-z_$][\w$]*)\.u\s*=\s*([A-Za-z_$][\w$]*)\s*=>\s*("static/chunks/"[^;]*?"\.js")`)
var entry = regexp.MustCompile(`^\s*"?([0-9]+)"?\s*:\s*"([A-Za-z0-9_./()\[\]-]+)"\s*$`)

func table(body string) (map[string]string, bool) {
	out := map[string]string{}
	if strings.TrimSpace(body) == "" {
		return out, true
	}
	for _, item := range strings.Split(body, ",") {
		m := entry.FindStringSubmatch(item)
		if m == nil {
			return nil, false
		}
		out[m[1]] = m[2]
	}
	return out, true
}

// Parse recognizes static filename resolvers without executing downloaded code.
// Unsupported expressions yield no inventory rather than guessed filenames.
func Parse(runtimeURL, body string) *Inventory {
	m := resolver.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	req, arg, expr := m[1], regexp.QuoteMeta(m[2]), m[3]
	aliasForm := regexp.MustCompile(`^"static/chunks/"\+\(\(\{([^{}]*)\}\)\[` + arg + `\]\|\|` + arg + `\)\+"([.-])"\+\(\{([^{}]*)\}\)\[` + arg + `\]\+"\.js"$`)
	simpleForm := regexp.MustCompile(`^"static/chunks/"\+` + arg + `\+"([.-])"\+\(\{([^{}]*)\}\)\[` + arg + `\]\+"\.js"$`)
	aliases := map[string]string{}
	var hashes map[string]string
	var sep string
	ok := false
	if parts := aliasForm.FindStringSubmatch(expr); parts != nil {
		aliases, ok = table(parts[1])
		if !ok {
			return nil
		}
		sep = parts[2]
		hashes, ok = table(parts[3])
	} else if parts := simpleForm.FindStringSubmatch(expr); parts != nil {
		sep = parts[1]
		hashes, ok = table(parts[2])
	}
	if !ok || len(hashes) == 0 || len(hashes) > 10000 {
		return nil
	}
	runtime, e := url.Parse(runtimeURL)
	if e != nil {
		return nil
	}
	public := ""
	p := regexp.MustCompile(regexp.QuoteMeta(req) + `\.p\s*=\s*"([^"\r\n]*)"`).FindStringSubmatch(body)
	if p != nil {
		public = p[1]
	} else if i := strings.Index(runtime.Path, "/_next/"); i >= 0 {
		public = runtime.Path[:i] + "/_next/"
	} else {
		return nil
	}
	root, e := runtime.Parse(public)
	if e != nil || (root.Scheme != "http" && root.Scheme != "https") {
		return nil
	}
	root.Path = strings.TrimRight(root.Path, "/") + "/"
	root.RawPath = ""
	root.RawQuery = ""
	root.Fragment = ""
	result := &Inventory{RuntimeURL: runtimeURL, PublicPath: root.String(), Chunks: map[string]string{}}
	for id, hash := range hashes {
		name := id
		if v, exists := aliases[id]; exists {
			name = v
		}
		ref, e := url.Parse("static/chunks/" + name + sep + hash + ".js")
		if e != nil {
			return nil
		}
		result.Chunks[id] = root.ResolveReference(ref).String()
	}
	return result
}
