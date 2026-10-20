package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Attributes keep both an unqualified name and a source-qualified name so
// batch STATUS and LIM STATUS, for example, never overwrite each other.
func (h *host) setAttribute(source, key, value string) {
	originalKey := strings.TrimSpace(key)
	key = strings.ToLower(originalKey)
	if key == "" {
		return
	}
	if h.attributes == nil {
		h.attributes = make(map[string]string)
	}
	h.attributes[source+"."+key] = value
	if key != "status" || source == "batch" {
		h.attributes[key] = value
	}
	if source == "static" || source == "load" {
		if h.resources == nil {
			h.resources = make(map[string]string)
		}
		h.resources[originalKey] = value
	}
	switch key {
	case "ncpus":
		h.cpu = value
	case "maxmem":
		h.memory = value
	case "osrel":
		h.osrel = value
	}
}

func (h *host) setResource(source, key, value string) {
	h.setAttribute(source, key, value)
	// Native select expressions and their resource names are case sensitive.
	if h.resources == nil {
		h.resources = make(map[string]string)
	}
	h.resources[key] = value
}

var resourceToken = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]*)(?:\s*[=:]\s*("[^"]*"|'[^']*'|[^\s(),\[\]]+))?`)

func (h *host) readResources(value string) {
	h.setAttribute("static", "resources", value)
	for _, match := range resourceToken.FindAllStringSubmatch(value, -1) {
		v := strings.Trim(match[2], `"'`)
		if v == "" {
			v = "1"
		}
		h.setResource("resource", match[1], v)
	}
	if match := osrelResource.FindStringSubmatch(value); h.osrel == "" && match != nil {
		h.setResource("resource", "osrel", match[1])
	}
}

var hostHeading = regexp.MustCompile(`^(?:HOST_NAME\s*:\s*|HOST\s*:?\s+)<?([^\s>]+)>?`)
var attributePair = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_./-]*)\s*:\s*`)

// Parse wide host tables and long host blocks without a fixed column list.
// Unknown columns are retained, including site-specific ELIM load indices.
func parseHostAttributes(text, source string) (map[string]host, error) {
	result := make(map[string]host)
	var current, section string
	var columns []string
	resourceLines := false
	recognized := false
	put := func(name, key, value string) {
		h := result[name]
		h.name = name
		if strings.EqualFold(key, "resources") {
			h.readResources(value)
		} else {
			h.setAttribute(source, key, value)
		}
		result[name] = h
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			columns = nil
			resourceLines = false
			continue
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "EXTERNAL") || strings.HasPrefix(upper, "ELIM RESOURCES") || strings.HasPrefix(upper, "SHARED RESOURCES") {
			columns = nil
			continue
		}
		if match := hostHeading.FindStringSubmatch(line); match != nil {
			if err := validateHost(match[1]); err != nil {
				return nil, err
			}
			current, section, columns, resourceLines = match[1], "", nil, false
			if _, exists := result[current]; !exists {
				result[current] = host{name: current}
			}
			recognized = true
			continue
		}
		if strings.Contains(line, "LOAD THRESHOLD") {
			section, columns = "threshold", nil
			continue
		}
		if strings.Contains(line, "CURRENT LOAD") {
			section, columns = "current", nil
			continue
		}
		fields, err := splitFilterTerms(line)
		if err != nil {
			return nil, fmt.Errorf("%s 主机字段: %w", source, err)
		}
		if len(fields) == 0 {
			continue
		}
		if strings.EqualFold(fields[0], "HOST_NAME") {
			columns, current, section = fields, "", ""
			recognized = true
			continue
		}
		if current == "" && len(columns) > 0 {
			minimum := len(columns)
			if strings.EqualFold(columns[len(columns)-1], "resources") {
				minimum--
			}
			if len(fields) < minimum {
				return nil, fmt.Errorf("%s 主机行缺少字段: %s", source, line)
			}
			if err := validateHost(fields[0]); err != nil {
				return nil, err
			}
			for i, key := range columns {
				value := ""
				if i < len(fields) {
					value = fields[i]
				}
				if strings.EqualFold(key, "resources") {
					value = strings.Join(fields[i:], " ")
				}
				put(fields[0], key, value)
			}
			continue
		}
		if current == "" {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(line), "RESOURCES:") {
			put(current, "resources", strings.TrimSpace(line[len("RESOURCES:"):]))
			resourceLines, columns = true, nil
			continue
		}
		pairs := attributePair.FindAllStringSubmatchIndex(line, -1)
		if len(pairs) > 0 {
			columns, resourceLines = nil, false
			for i, p := range pairs {
				key := line[p[2]:p[3]]
				end := len(line)
				if i+1 < len(pairs) {
					end = pairs[i+1][0]
				}
				value := strings.TrimSpace(line[p[1]:end])
				if value != "" {
					put(current, key, strings.Trim(value, "<>"))
				}
				if strings.EqualFold(key, "resources") {
					resourceLines = true
				}
			}
			continue
		}
		if line == "RESOURCES" {
			resourceLines, columns = true, nil
			continue
		}
		if resourceLines {
			h := result[current]
			previous := h.attributes["static.resources"]
			h.readResources(strings.TrimSpace(previous + " " + line))
			result[current] = h
			continue
		}
		if len(columns) > 0 {
			offset, prefix := 0, ""
			switch strings.ToLower(fields[0]) {
			case "loadsched", "loadstop", "total", "reserved":
				offset, prefix = 1, strings.ToLower(fields[0])+"."
			}
			if len(fields)-offset == len(columns) || (len(fields)-offset >= len(columns) && strings.EqualFold(columns[len(columns)-1], "resources")) {
				for i, key := range columns {
					value := fields[i+offset]
					if strings.EqualFold(key, "resources") {
						value = strings.Join(fields[i+offset:], " ")
					}
					if section == "current" && prefix == "" {
						prefix = "current."
					}
					put(current, prefix+key, value)
				}
				continue
			}
		}
		// A section title is not a data row. Headers contain identifiers only.
		header := len(fields) > 0
		for _, field := range fields {
			header = header && resourceNamePattern.MatchString(strings.ReplaceAll(field, "/", "_"))
		}
		if header {
			columns = fields
		}
	}
	if !recognized {
		return nil, fmt.Errorf("无法识别 %s 主机输出：需要 HOST_NAME 表头或 HOST: 主机块", source)
	}
	return result, nil
}

// Shared resource tables associate each instance with its LOCATION hosts.
// A continued LOCATION belongs to the previous instance, never to all hosts.
var noSharedResources = regexp.MustCompile(`(?i)no (?:static |dynamic |shared )*resources?`)

func parseSharedResources(text string, hosts []host, source string) error {
	recognized := false
	resourceStart, valueStart := 0, 0
	var name, value string
	for _, raw := range strings.Split(text, "\n") {
		fields, err := splitFilterTerms(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "RESOURCE" || fields[0] == "RESOURCE_NAME" {
			recognized = len(fields) >= 3 && fields[1] == "VALUE" && fields[2] == "LOCATION"
			resourceStart, valueStart = strings.Index(raw, fields[0]), strings.Index(raw, "VALUE")
			continue
		}
		if !recognized {
			continue
		}
		locations := fields
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if len(fields) >= 3 && resourceNamePattern.MatchString(fields[0]) && indent <= resourceStart {
			name, value, locations = fields[0], fields[1], fields[2:]
		} else if len(fields) >= 2 && (indent <= valueStart || strings.HasPrefix(fields[1], "(")) {
			value, locations = fields[0], fields[1:]
		}
		if name == "" {
			return fmt.Errorf("%s 资源续行缺少名称", source)
		}
		for _, location := range locations {
			location = strings.Trim(location, "(),[]")
			for i := range hosts {
				if hosts[i].name == location || location == "all" {
					hosts[i].setResource(source, name, value)
				}
			}
		}
	}
	if !recognized && !noSharedResources.MatchString(text) {
		return fmt.Errorf("无法识别 %s 共享资源输出：需要 RESOURCE VALUE LOCATION", source)
	}
	return nil
}

var standardHostFields = strings.Fields("type model ostype nprocs ncores nthreads cpuf maxswp maxtmp server resources r15s r1m r15m ut mem swp pg io it tmp jl/u jl/p ssusp ususp groups")

func hostFields(hosts []host) []string {
	seen := make(map[string]bool)
	for _, key := range standardHostFields {
		seen[key] = true
	}
	for _, h := range hosts {
		for key := range h.attributes {
			seen[key] = true
		}
		for key := range h.resources {
			seen[strings.ToLower(key)] = true
		}
	}
	var result []string
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func hostSnapshot(h host) string {
	lines := []string{"主机列表快照（返回 Hosts 后按 r 更新）", "CPU: " + hostValue(h.cpu), "Memory: " + hostValue(h.memory), "OSREL: " + hostValue(h.osrel), "Queues: " + hostValue(strings.Join(h.queues, ", "))}
	for _, key := range hostFields([]host{h}) {
		value, _ := hostField(h, key)
		lines = append(lines, key+": "+hostValue(value))
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func hostTableFields(hosts []host) []string {
	var result []string
	for _, key := range hostFields(hosts) {
		if strings.Contains(" host_name status ncpus maxmem osrel max njobs run rsv ", " "+key+" ") {
			continue
		}
		if source, rest, qualified := strings.Cut(key, "."); qualified && (source == "static" || source == "load" || source == "batch" || source == "resource" || source == "external") {
			same := key != "load.status"
			for _, h := range hosts {
				value, exists := h.attributes[key]
				plain, _ := hostField(h, rest)
				if exists && value != plain {
					same = false
				}
			}
			if same {
				continue
			}
		}
		result = append(result, key)
	}
	return result
}
