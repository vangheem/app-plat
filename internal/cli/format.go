package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func render(v any, text bool) (string, error) {
	if text {
		return renderText(v), nil
	}
	return marshalJSON(v)
}

func marshalJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func renderText(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return string(raw)
	}
	return formatValue(generic, 0)
}

func formatValue(v any, indent int) string {
	pad := strings.Repeat("  ", indent)
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			switch child := t[k].(type) {
			case map[string]any:
				fmt.Fprintf(&b, "%s%s:\n%s", pad, k, formatValue(child, indent+1))
			case []any:
				fmt.Fprintf(&b, "%s%s: %s\n", pad, k, formatInline(child))
			default:
				fmt.Fprintf(&b, "%s%s: %v\n", pad, k, t[k])
			}
		}
		return b.String()
	case []any:
		if len(t) == 0 {
			return pad + "(none)\n"
		}
		var b strings.Builder
		for i, item := range t {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(formatValue(item, indent))
		}
		return b.String()
	default:
		return fmt.Sprintf("%s%v\n", pad, v)
	}
}

func formatInline(items []any) string {
	if len(items) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			parts = append(parts, compactMap(m))
			continue
		}
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, ", ")
}

func compactMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " ")
}
