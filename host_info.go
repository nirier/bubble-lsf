package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var osrelResource = regexp.MustCompile(`(?i)(?:^|[\s(\[])osrel(?:[=:]\s*|[_-]?)([a-z0-9][a-z0-9_.-]*)`)

// Use the wide table available on older LSF clients as well as newer ones.
// Some sites expose OSREL as a column, others as a named resource.
func parseHostInfo(text string) (map[string]host, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	columns := make(map[string]int)
	for i, field := range strings.Fields(lines[0]) {
		columns[strings.ToLower(field)] = i
	}
	for _, name := range []string{"host_name", "ncpus", "maxmem"} {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("无法识别 lshosts -w 输出：缺少 %s 列", name)
		}
	}
	result := make(map[string]host)
	for i, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		get := func(name string) string {
			if n, ok := columns[name]; ok && n < len(fields) {
				return fields[n]
			}
			return ""
		}
		h := host{name: get("host_name"), cpu: get("ncpus"), memory: get("maxmem"), osrel: get("osrel")}
		if err := validateHost(h.name); err != nil || h.cpu == "" || h.memory == "" {
			return nil, fmt.Errorf("lshosts 第 %d 行格式不支持", i+2)
		}
		for n, column := range strings.Fields(lines[0]) {
			key := strings.ToLower(column)
			if n >= len(fields) {
				continue
			}
			if key == "resources" {
				h.readResources(strings.Join(fields[n:], " "))
			} else {
				h.setAttribute("static", key, fields[n])
			}
		}
		if explicit := get("osrel"); explicit != "" {
			h.osrel = explicit
		}
		if h.osrel != "" {
			h.setResource("resource", "osrel", h.osrel)
		}
		result[h.name] = h
	}
	return result, nil
}

type queueHosts struct {
	name  string
	hosts []string
}

var queueField = regexp.MustCompile(`^([A-Z][A-Z0-9_ /-]*):\s*(.*)$`)

func parseQueueHosts(text string) ([]queueHosts, error) {
	var queues []queueHosts
	var current *queueHosts
	readingHosts, foundHosts := false, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if field := queueField.FindStringSubmatch(line); field != nil {
			readingHosts = false
			switch field[1] {
			case "QUEUE":
				if current != nil && !foundHosts {
					return nil, fmt.Errorf("queue %s 缺少 HOSTS 配置", current.name)
				}
				queues = append(queues, queueHosts{name: field[2]})
				current = &queues[len(queues)-1]
				foundHosts = false
			case "HOSTS":
				if current != nil {
					current.hosts = strings.Fields(field[2])
					readingHosts, foundHosts = true, true
				}
			}
		} else if readingHosts {
			if line == "" {
				readingHosts = false
			} else {
				current.hosts = append(current.hosts, strings.Fields(line)...)
			}
		}
	}
	if current == nil || !foundHosts {
		return nil, errors.New("无法识别 bqueues -l 输出：需要 QUEUE 和 HOSTS 配置")
	}
	return queues, nil
}

func parseHostGroups(text string) (map[string][]string, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if header := strings.Fields(lines[0]); len(header) != 2 || header[0] != "GROUP_NAME" || header[1] != "HOSTS" {
		return nil, errors.New("无法识别 bmgroup -w -r 输出：需要 GROUP_NAME HOSTS 表头")
	}
	groups := make(map[string][]string)
	var name string
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			name, fields = strings.TrimSuffix(fields[0], "/"), fields[1:]
		}
		if name == "" {
			return nil, errors.New("bmgroup 主机组续行缺少组名")
		}
		groups[name] = append(groups[name], fields...)
	}
	return groups, nil
}

func queueHostToken(token string) (name string, excluded bool) {
	name = strings.TrimPrefix(token, "~")
	excluded = name != token
	// A host preference (host+1) does not change membership.
	if before, _, ok := strings.Cut(name, "+"); ok {
		name = before
	}
	return name, excluded
}

func assignQueues(hosts []host, queues []queueHosts, groups map[string][]string) error {
	var failures []error
	for _, queue := range queues {
		included, excluded := make(map[string]bool), make(map[string]bool)
		all, valid := false, true
		for _, token := range queue.hosts {
			name, exclude := queueHostToken(token)
			names := []string{name}
			if strings.HasSuffix(name, "/") {
				var ok bool
				names, ok = groups[strings.TrimSuffix(name, "/")]
				if !ok {
					failures = append(failures, fmt.Errorf("queue %s 的主机组 %s 无法展开", queue.name, name))
					valid = false
					break
				}
			}
			for _, name := range names {
				if (name == "all" || name == "others") && !exclude {
					all = true
				} else if exclude {
					excluded[name] = true
				} else {
					included[name] = true
				}
			}
		}
		if !valid {
			continue
		}
		for i := range hosts {
			if (all || included[hosts[i].name]) && !excluded[hosts[i].name] && !excluded["all"] {
				hosts[i].queues = append(hosts[i].queues, queue.name)
			}
		}
	}
	for i := range hosts {
		sort.Strings(hosts[i].queues)
	}
	return errors.Join(failures...)
}

func enrichHosts(ctx context.Context, hosts []host) ([]host, error) {
	specs := []commandSpec{
		{name: "lshosts", args: []string{"-w"}},
		{name: "lshosts", args: []string{"-l"}},
		{name: "bhosts", args: []string{"-l"}},
		{name: "lsload", args: []string{"-l"}},
		{name: "lshosts", args: []string{"-s"}},
		{name: "lsload", args: []string{"-s"}},
		{name: "bqueues", args: []string{"-l"}},
		{name: "bmgroup", args: []string{"-w", "-r"}},
	}
	type result struct {
		out commandOutput
		err error
	}
	results := make([]result, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func() { defer wg.Done(); results[i].out, results[i].err = runLSF(ctx, spec) }()
	}
	wg.Wait()
	var failures []error
	var queues []queueHosts
	var groups map[string][]string
	for i, r := range results {
		err := r.err
		if err == nil {
			switch i {
			case 0, 1, 2, 3:
				var info map[string]host
				if i == 0 {
					info, err = parseHostInfo(r.out.stdout)
				} else {
					info, err = parseHostAttributes(r.out.stdout, []string{"", "static", "batch", "load"}[i])
				}
				if err == nil {
					for n := range hosts {
						h, ok := info[hosts[n].name]
						if !ok {
							continue
						}
						if hosts[n].attributes == nil {
							hosts[n].attributes = make(map[string]string)
						}
						for key, value := range h.attributes {
							hosts[n].attributes[key] = value
						}
						if hosts[n].resources == nil {
							hosts[n].resources = make(map[string]string)
						}
						for key, value := range h.resources {
							hosts[n].resources[key] = value
						}
						if h.cpu != "" {
							hosts[n].cpu = h.cpu
						}
						if h.memory != "" {
							hosts[n].memory = h.memory
						}
						if h.osrel != "" {
							hosts[n].osrel = h.osrel
						}
					}
				}
			case 4, 5:
				err = parseSharedResources(r.out.stdout, hosts, []string{"resource", "external"}[i-4])
			case 6:
				queues, err = parseQueueHosts(r.out.stdout)
			case 7:
				groups, err = parseHostGroups(r.out.stdout)
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %w", specs[i].name, strings.Join(specs[i].args, " "), err))
		}
	}
	if queues != nil {
		failures = append(failures, assignQueues(hosts, queues, groups))
	}
	for name, members := range groups {
		for _, member := range members {
			for i := range hosts {
				if member == hosts[i].name {
					hosts[i].groups = append(hosts[i].groups, name)
				}
			}
		}
	}
	for i := range hosts {
		sort.Strings(hosts[i].groups)
		h := &hosts[i]
		for key, target := range map[string]*string{"status": &h.status, "max": &h.max, "njobs": &h.jobs, "run": &h.running, "rsv": &h.reserved} {
			if value, ok := h.attributes["batch."+key]; ok {
				*target = value
			}
		}
		ssusp, sok := h.attributes["batch.ssusp"]
		ususp, uok := h.attributes["batch.ususp"]
		if sok && uok {
			h.suspended = ssusp + "/" + ususp
		}
	}
	return hosts, errors.Join(failures...)
}
