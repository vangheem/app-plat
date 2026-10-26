package cli

import (
	"fmt"
	"strings"
)

type parsedArgs struct {
	strings map[string]string
	bools   map[string]bool
	args    []string
}

func parseArgs(args, stringFlags, boolFlags []string) (parsedArgs, error) {
	strSet := map[string]bool{}
	for _, name := range stringFlags {
		strSet[name] = true
	}
	boolSet := map[string]bool{}
	for _, name := range boolFlags {
		boolSet[name] = true
	}
	out := parsedArgs{
		strings: map[string]string{},
		bools:   map[string]bool{},
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out.args = append(out.args, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "--") {
			out.args = append(out.args, a)
			continue
		}
		name := strings.TrimPrefix(a, "--")
		val := ""
		hasVal := false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			val = name[eq+1:]
			name = name[:eq]
			hasVal = true
		}
		if name == "" {
			return parsedArgs{}, fmt.Errorf("invalid flag %q", a)
		}
		switch {
		case boolSet[name]:
			if hasVal {
				out.bools[name] = isTruthy(val)
			} else {
				out.bools[name] = true
			}
		case strSet[name]:
			if !hasVal {
				if i+1 >= len(args) || (strings.HasPrefix(args[i+1], "--") && args[i+1] != "--") {
					return parsedArgs{}, fmt.Errorf("flag --%s requires a value", name)
				}
				i++
				val = args[i]
			}
			out.strings[name] = val
		default:
			return parsedArgs{}, fmt.Errorf("unknown flag --%s", name)
		}
	}
	return out, nil
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

// extractGlobal pulls --text and --api-url from anywhere in args.
func extractGlobal(args []string) (apiURL string, text bool, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--text":
			text = true
		case a == "--api-url":
			if i+1 >= len(args) {
				return "", false, nil, fmt.Errorf("--api-url requires a value")
			}
			i++
			apiURL = args[i]
		case strings.HasPrefix(a, "--api-url="):
			apiURL = strings.TrimPrefix(a, "--api-url=")
			if apiURL == "" {
				return "", false, nil, fmt.Errorf("--api-url requires a value")
			}
		default:
			rest = append(rest, a)
		}
	}
	return apiURL, text, rest, nil
}

// FirstCommand is the first non-global token, used to detect `mcp`.
func FirstCommand(args []string) string {
	_, _, rest, err := extractGlobal(args)
	if err != nil || len(rest) == 0 {
		return ""
	}
	return rest[0]
}

// APIURLFlag returns a global --api-url value, if one was passed.
func APIURLFlag(args []string) (string, error) {
	url, _, _, err := extractGlobal(args)
	return url, err
}
