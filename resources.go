package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type resourceDescriptor struct {
	name, kind, description, order, interval, builtin, dynamic, consumable, release string
}

// Offline descriptions are a fallback; the cluster catalog replaces them.
func builtinResources() []resourceDescriptor {
	return []resourceDescriptor{
		{name: "ncpus", kind: "Numeric", description: "CPU 数量；PARALLEL_SCHED_BY_SLOT=Y 时 select 中表示槽位数"},
		{name: "maxmem", kind: "Numeric", description: "主机总内存，LSF 原生单位"},
		{name: "mem", kind: "Numeric", description: "可用内存；默认 MB，遵循集群单位配置", dynamic: "Yes"},
		{name: "swp", kind: "Numeric", description: "可用交换空间", dynamic: "Yes"},
		{name: "tmp", kind: "Numeric", description: "可用临时磁盘空间", dynamic: "Yes"},
		{name: "ut", kind: "Numeric", description: "CPU 利用率，条件中使用 0.0–1.0", dynamic: "Yes"},
		{name: "r1m", kind: "Numeric", description: "1 分钟平均 CPU 运行队列长度；cpu 是此资源的别名", dynamic: "Yes"},
		{name: "type", kind: "String", description: "主机类型；any 表示任意类型"},
		{name: "model", kind: "String", description: "主机型号"},
		{name: "hname", kind: "String", description: "主机名"},
		{name: "status", kind: "String", description: "bhosts 主机状态"},
		{name: "server", kind: "Boolean", description: "是否为 LSF server 主机"},
	}
}

var resourceNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

func parseResourceCatalog(text string) ([]resourceDescriptor, error) {
	var result []resourceDescriptor
	var current *resourceDescriptor
	var columns []string
	descriptionLine := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		key, value, hasColon := strings.Cut(line, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if hasColon && key == "RESOURCE_NAME" {
			if !resourceNamePattern.MatchString(value) {
				return nil, fmt.Errorf("lsinfo 资源名称无效 %q", value)
			}
			result = append(result, resourceDescriptor{name: value})
			current = &result[len(result)-1]
			columns, descriptionLine = nil, false
			continue
		}
		if current == nil {
			continue
		}
		set := func(key, value string) {
			switch key {
			case "TYPE":
				current.kind = value
			case "ORDER":
				current.order = value
			case "INTERVAL":
				current.interval = value
			case "BUILTIN":
				current.builtin = value
			case "DYNAMIC":
				current.dynamic = value
			case "CONSUMABLE":
				current.consumable = value
			case "RELEASE":
				current.release = value
			case "DESCRIPTION":
				current.description = value
			}
		}
		if hasColon {
			set(key, value)
			descriptionLine = key == "DESCRIPTION"
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "TYPE" {
			columns, descriptionLine = fields, false
		} else if len(columns) > 0 && len(fields) == len(columns) {
			for i, column := range columns {
				set(column, fields[i])
			}
			columns = nil
		} else if descriptionLine && line != "" {
			current.description += " " + line
		} else if line == "" {
			descriptionLine = false
		}
	}
	if len(result) == 0 {
		return nil, errors.New("无法识别 lsinfo -r -l 输出：需要 RESOURCE_NAME、TYPE 和 DESCRIPTION")
	}
	seen := make(map[string]bool)
	for _, r := range result {
		if seen[r.name] || (strings.ToLower(r.kind) != "numeric" && strings.ToLower(r.kind) != "string" && strings.ToLower(r.kind) != "boolean") {
			return nil, fmt.Errorf("lsinfo 资源 %s 的 TYPE 缺失、无效或名称重复", r.name)
		}
		seen[r.name] = true
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func listResourceCatalog(ctx context.Context) ([]resourceDescriptor, error) {
	out, err := runLSF(ctx, commandSpec{name: "lsinfo", args: []string{"-r", "-l"}})
	if err != nil {
		return nil, err
	}
	return parseResourceCatalog(out.stdout)
}

// Only accept a selection section for host lookup: bhosts ignores rusage.
// LSF performs the actual expression and resource-name validation.
func selectionRequirement(value string) (string, error) {
	if len(value) > 4096 || strings.ContainsFunc(value, unicode.IsControl) {
		return "", errors.New("资源条件不能超过 4096 字符或包含控制字符")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "select[") {
		if !strings.HasSuffix(value, "]") {
			return "", errors.New("select 条件缺少结束的 ]")
		}
		value = strings.TrimSpace(value[len("select[") : len(value)-1])
	}
	if value == "" {
		return "", errors.New("select 条件不能为空；清空输入可取消资源筛选")
	}
	var quote rune
	depth := 0
	for _, r := range value {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '[', ']', '{', '}':
			return "", errors.New("这里只支持 select 条件；rusage、span 等不能用于筛选主机")
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", errors.New("资源条件括号不匹配")
			}
		}
	}
	if quote != 0 || depth != 0 {
		return "", errors.New("资源条件引号或括号不匹配")
	}
	return "select[" + value + "]", nil
}

func executionHosts(value string) []string {
	var result []string
	for _, name := range strings.FieldsFunc(value, func(r rune) bool { return r == ':' || r == ',' || r == ' ' }) {
		if _, after, ok := strings.Cut(name, "*"); ok {
			name = after
		}
		if validateHost(name) == nil {
			result = append(result, name)
		}
	}
	return result
}

func jobsOnHosts(jobs []job, hosts []host) []job {
	allowed := make(map[string]bool)
	for _, h := range hosts {
		allowed[h.name] = true
	}
	var result []job
	for _, j := range jobs {
		for _, name := range executionHosts(j.host) {
			if allowed[name] {
				result = append(result, j)
				break
			}
		}
	}
	return result
}

func listJobsWithRequirement(ctx context.Context, requirement string) ([]job, error) {
	jobs, err := listJobs(ctx)
	if err != nil || requirement == "" {
		return jobs, err
	}
	hosts, err := selectHosts(ctx, requirement)
	if err != nil {
		return nil, err
	}
	return jobsOnHosts(jobs, hosts), nil
}
