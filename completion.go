package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
)

type completion struct {
	value, description string
	start, end         int
}

var completionValue = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]*)\s*(?:==|!=|>=|<=|>|<|=|:)\s*(['"]?)([^\s'"&|()\[\]=<>!:]*)$`)
var localCompletionValue = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_./-]*)\s*(?:!=|>=|<=|>|<|=|:)\s*(['"]?)([^\s'"=<>!:]*)$`)

func identifier(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-'
}

func completeInput(input textinput.Model, native bool, descriptors []resourceDescriptor, values map[string][]string, fields map[string]string) []completion {
	runes := []rune(input.Value())
	pos := min(input.Position(), len(runes))
	prefix := string(runes[:pos])
	start, end := pos, pos
	isIdentifier := func(r rune) bool { return identifier(r) || (!native && r == '/') }
	for start > 0 && isIdentifier(runes[start-1]) {
		start--
	}
	for end < len(runes) && isIdentifier(runes[end]) {
		end++
	}
	fragment := string(runes[start:pos])
	var result []completion
	pattern := completionValue
	if !native {
		pattern = localCompletionValue
	}
	if match := pattern.FindStringSubmatch(prefix); match != nil {
		field, quote, fragment := match[1], match[2], match[3]
		if !native {
			field = strings.ToLower(field)
			for end < len(runes) && (runes[end] == '[' || runes[end] == ']' || isIdentifier(runes[end])) {
				end++
			}
		}
		start = pos - len([]rune(fragment+quote))
		if quote != "" && end < len(runes) && string(runes[end]) == quote {
			end++
		}
		stringResource, numericResource := false, false
		for _, r := range descriptors {
			if r.name == field {
				stringResource = strings.EqualFold(r.kind, "string")
				numericResource = strings.EqualFold(r.kind, "numeric")
			}
		}
		seen := make(map[string]bool)
		for _, value := range values[field] {
			if value == "" || value == "-" || seen[value] || strings.ContainsAny(value, "\r\n\t'\"") {
				continue
			}
			if native && numericResource {
				if _, err := strconv.ParseFloat(value, 64); err != nil {
					continue
				}
			}
			seen[value] = true
			matches := strings.HasPrefix(value, fragment)
			if !native {
				matches = strings.HasPrefix(strings.ToLower(value), strings.ToLower(fragment))
			}
			if matches {
				if (native && stringResource) || quote != "" || (!native && strings.ContainsAny(value, " \t")) {
					if quote == "" {
						quote = "'"
					}
					value = quote + value + quote
				}
				result = append(result, completion{value: value, description: field + " 的已知值", start: start, end: end})
			}
		}
	} else if native {
		for _, r := range descriptors {
			if strings.HasPrefix(r.name, fragment) {
				value := r.name
				// Preserve operators already following the cursor.
				if end == len(runes) || !strings.ContainsRune("=<>!", runes[end]) {
					switch strings.ToLower(r.kind) {
					case "numeric":
						value += ">="
					case "string":
						value += "=="
					}
				}
				result = append(result, completion{value: value, description: r.kind + " · " + r.description, start: start, end: end})
			}
		}
		if strings.HasPrefix("select", fragment) && fragment != "" {
			value := "select["
			if end < len(runes) && runes[end] == '[' {
				value = "select"
			}
			result = append(result, completion{value: value, description: "LSF 主机选择表达式", start: start, end: end})
		}
	} else {
		for field, description := range fields {
			if strings.HasPrefix(field, strings.ToLower(fragment)) {
				value := field
				if end < len(runes) && strings.ContainsRune(":=<>!", runes[end]) {
					value = strings.TrimRight(value, ":=<>!")
				}
				result = append(result, completion{value: value, description: description, start: start, end: end})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].value < result[j].value })
	return result
}

func applyCompletion(input *textinput.Model, c completion) {
	runes := []rune(input.Value())
	value := string(runes[:c.start]) + c.value + string(runes[c.end:])
	input.SetValue(value)
	input.SetCursor(c.start + len([]rune(c.value)))
}
