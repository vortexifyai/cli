package projectfiles

import (
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var inlineLink = regexp.MustCompile(`\]\(\s*<?([^\s)>]+)`)
var definitionLink = regexp.MustCompile(`^\s{0,3}\[[^]]+\]:\s*<?([^\s>]+)`)
var inlineCode = regexp.MustCompile("`+[^`]*`+")

// Warn about workspace-dependent links; never rewrite authored documents.
func warnSourceLinks(out io.Writer, collection, file, text string) {
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		line = inlineCode.ReplaceAllString(line, "")
		links := inlineLink.FindAllStringSubmatch(line, -1)
		links = append(links, definitionLink.FindAllStringSubmatch(line, -1)...)
		for _, match := range links {
			href := match[1]
			target, err := url.Parse(href)
			if err != nil || target.Scheme != "" || target.Host != "" || target.Path == "" {
				continue
			}
			resolved := path.Clean(path.Join(".vortexify", collection, path.Dir(file), target.Path))
			if strings.HasPrefix(target.Path, "/") {
				resolved = strings.TrimPrefix(path.Clean(target.Path), "/")
			}
			if strings.HasPrefix(resolved, ".vortexify/context/") || strings.HasPrefix(resolved, ".vortexify/knowledge-base/") {
				continue
			}
			fmt.Fprintf(out, "Warning: %s/%s links to local path %q; use a resource_reference from 'vortexify registry --json' or 'vortexify tables --json' for portable resource navigation.\n", collection, file, href)
		}
	}
}
