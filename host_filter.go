package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type hostPredicate func(host) bool

var filterTerm = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_./-]*)(>=|<=|!=|=|>|<|:)(.+)$`)
var memoryValue = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)([kmgtpe]?)(?:i?b)?$`)

func memoryBytes(value string) (float64, bool) {
	match := memoryValue.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	// LSF maxmem values use binary units. A bare filter value means GiB.
	unit := strings.ToUpper(match[2])
	if unit == "" && !strings.HasSuffix(strings.ToLower(value), "b") {
		unit = "G"
	}
	if unit == "" {
		return n, true
	}
	for i := 0; i <= strings.Index("KMGTPE", unit); i++ {
		n *= 1024
	}
	return n, true
}

func hostField(h host, field string) (string, bool) {
	switch strings.ToLower(field) {
	case "host", "name":
		return h.name, true
	case "status":
		return h.status, true
	case "cpu", "ncpus":
		return h.cpu, true
	case "memory", "maxmem":
		return h.memory, true
	case "osrel":
		return h.osrel, true
	case "queue", "queues":
		return strings.Join(h.queues, ","), true
	case "group", "groups", "hostgroup":
		return strings.Join(h.groups, ","), true
	case "max":
		return h.max, true
	case "jobs", "njobs":
		return h.jobs, true
	case "run":
		return h.running, true
	case "suspended":
		return h.suspended, true
	case "reserved", "rsv":
		return h.reserved, true
	}
	field = strings.ToLower(field)
	if value, ok := h.attributes[field]; ok {
		return value, true
	}
	for key, value := range h.resources {
		if strings.EqualFold(key, field) {
			return value, true
		}
	}
	for _, key := range standardHostFields {
		if key == field {
			return "", true
		}
	}
	return "", false
}

func hostMemoryField(field string) bool {
	field = field[strings.LastIndex(field, ".")+1:]
	switch field {
	case "memory", "maxmem", "maxswp", "maxtmp", "mem", "swp", "tmp":
		return true
	}
	return false
}

func hostNumericField(field string) bool {
	field = field[strings.LastIndex(field, ".")+1:]
	return hostMemoryField(field) || strings.Contains(" cpu ncpus nprocs ncores nthreads cpuf max jobs njobs run ssusp ususp reserved rsv jl/u jl/p r15s r1m r15m ut pg io it ", " "+field+" ")
}

func hostNumber(value string) (float64, bool) {
	value = strings.TrimLeft(strings.TrimSpace(value), "*")
	percent := strings.HasSuffix(value, "%")
	n, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if percent {
		n /= 100
	}
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func compileHostFilter(query string, hosts ...host) ([]hostPredicate, error) {
	terms, err := splitFilterTerms(strings.ToLower(query))
	if err != nil {
		return nil, err
	}
	kinds := make(map[string]string)
	for _, h := range hosts {
		for key, kind := range h.resourceKinds {
			kinds[strings.ToLower(key)] = kind
		}
	}
	known := make(map[string]bool)
	for _, field := range hostFields(hosts) {
		known[field] = true
	}
	var predicates []hostPredicate
	for _, term := range terms {
		match := filterTerm.FindStringSubmatch(term)
		if match == nil {
			if strings.ContainsAny(term, ":=<>!") {
				return nil, fmt.Errorf("无效过滤条件 %q", term)
			}
			predicates = append(predicates, func(h host) bool {
				text := []string{h.name, h.status, h.cpu, h.memory, h.osrel, strings.Join(h.queues, ","), strings.Join(h.groups, ","), h.max, h.jobs, h.running, h.suspended, h.reserved}
				for key, value := range h.attributes {
					text = append(text, key, value)
				}
				for key, value := range h.resources {
					text = append(text, key, value)
				}
				return strings.Contains(strings.ToLower(strings.Join(text, " ")), term)
			})
			continue
		}
		field, op, value := match[1], match[2], match[3]
		if _, ok := hostField(host{}, field); !ok && !known[field] {
			return nil, fmt.Errorf("未知字段 %q；支持主机内置字段及已加载的自定义资源，Tab 查看候选", field)
		}
		kind := kinds[field]
		if source, name, ok := strings.Cut(field, "."); ok && (source == "resource" || source == "external" || source == "load" || source == "static") {
			kind = kinds[name]
		}
		numeric := hostNumericField(field) || strings.EqualFold(kind, "Numeric") || strings.ContainsAny(op, "><")
		if numeric && op != ":" {
			parse := hostNumber
			if hostMemoryField(field) {
				parse = func(raw string) (float64, bool) { return memoryBytes(strings.TrimLeft(raw, "*")) }
			}
			want, ok := parse(value)
			if !ok || (hostNumericField(field) && want < 0) {
				return nil, fmt.Errorf("%s 需要有效数值；内存示例 memory>=64G，利用率 ut>=80%%", field)
			}
			predicates = append(predicates, func(h host) bool {
				raw, _ := hostField(h, field)
				got, valid := parse(raw)
				if !valid {
					return false
				}
				switch op {
				case "=":
					return got == want
				case "!=":
					return got != want
				case ">=":
					return got >= want
				case "<=":
					return got <= want
				case ">":
					return got > want
				case "<":
					return got < want
				}
				return false
			})
		} else {
			if op != ":" && op != "=" && op != "!=" {
				return nil, fmt.Errorf("%s 支持 : 包含、= 等于、!= 不等于", field)
			}
			predicates = append(predicates, func(h host) bool {
				raw, _ := hostField(h, field)
				if raw == "" || raw == "-" {
					return false
				}
				if op == ":" {
					return strings.Contains(strings.ToLower(raw), value)
				}
				equal := strings.EqualFold(raw, value)
				var members []string
				switch field {
				case "queue", "queues":
					members = h.queues
				case "group", "groups", "hostgroup":
					members = h.groups
				}
				for _, member := range members {
					equal = equal || strings.EqualFold(member, value)
				}
				return equal == (op == "=")
			})
		}
	}
	return predicates, nil
}

func splitFilterTerms(query string) ([]string, error) {
	var terms []string
	var token strings.Builder
	var quote rune
	escaped := false
	for _, r := range query {
		if escaped {
			token.WriteRune(r)
			escaped = false
		} else if quote != 0 {
			if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
		} else if r == '\'' || r == '"' {
			quote = r
		} else if unicode.IsSpace(r) {
			if token.Len() > 0 {
				terms = append(terms, token.String())
				token.Reset()
			}
		} else {
			token.WriteRune(r)
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("过滤条件引号不匹配")
	}
	if token.Len() > 0 {
		terms = append(terms, token.String())
	}
	return terms, nil
}

func matchesHost(h host, predicates []hostPredicate) bool {
	for _, predicate := range predicates {
		if !predicate(h) {
			return false
		}
	}
	return true
}
