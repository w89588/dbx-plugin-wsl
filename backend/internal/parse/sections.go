package parse

import "strings"

// ParseSections splits marked script output into named sections. `markers`
// lists the exact marker lines, e.g. "@@KERNEL@@". Section content keeps the
// trailing newline of each line. Text before the first marker is ignored
// (login shells may print profile output).
func ParseSections(output string, markers []string) map[string]string {
	known := make(map[string]struct{}, len(markers))
	for _, marker := range markers {
		known[marker] = struct{}{}
	}
	sections := map[string]string{}
	current := ""
	var builder strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = builder.String()
		}
	}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if _, ok := known[trimmed]; ok {
			flush()
			current = trimmed
			builder.Reset()
			continue
		}
		if current == "" {
			continue
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	flush()
	return sections
}

// ParseKeyValueBlock parses `KEY=VALUE` lines (e.g. /etc/os-release).
func ParseKeyValueBlock(text string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		index := strings.IndexByte(line, '=')
		if index <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:index])
		value := strings.TrimSpace(line[index+1:])
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	return values
}
