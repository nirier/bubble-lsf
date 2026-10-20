package main

import (
	"fmt"
	"strings"
)

type jobPredicate func(job) bool

func jobField(j job, field string) (string, bool) {
	switch field {
	case "id", "jobid":
		return j.id, true
	case "user":
		return j.user, true
	case "status", "stat":
		return j.status, true
	case "queue":
		return j.queue, true
	case "host", "exec_host":
		return j.host, true
	case "name", "job_name":
		return j.name, true
	case "submit", "submit_time", "submitted":
		return j.submitted, true
	}
	return "", false
}

func compileJobFilter(query string) ([]jobPredicate, error) {
	terms, err := splitFilterTerms(strings.ToLower(query))
	if err != nil {
		return nil, err
	}
	var result []jobPredicate
	for _, term := range terms {
		match := filterTerm.FindStringSubmatch(term)
		if match == nil {
			if strings.ContainsAny(term, ":=<>!") {
				return nil, fmt.Errorf("无效过滤条件 %q", term)
			}
			result = append(result, func(j job) bool {
				return strings.Contains(strings.ToLower(strings.Join([]string{j.id, j.user, j.status, j.queue, j.host, j.name, j.submitted}, " ")), term)
			})
			continue
		}
		field, op, value := match[1], match[2], match[3]
		if _, ok := jobField(job{}, field); !ok {
			return nil, fmt.Errorf("未知 Jobs 字段 %q；可用 id、user、status、queue、host、name、submit", field)
		}
		if op != ":" && op != "=" && op != "!=" {
			return nil, fmt.Errorf("Jobs 字段支持 : 包含、= 等于、!= 不等于")
		}
		result = append(result, func(j job) bool {
			raw, _ := jobField(j, field)
			raw = strings.ToLower(raw)
			if op == ":" {
				return strings.Contains(raw, value)
			}
			equal := raw == value
			if field == "host" || field == "exec_host" {
				for _, name := range executionHosts(j.host) {
					equal = equal || strings.EqualFold(name, value)
				}
			}
			return equal == (op == "=")
		})
	}
	return result, nil
}

func matchesJob(j job, filters []jobPredicate) bool {
	for _, filter := range filters {
		if !filter(j) {
			return false
		}
	}
	return true
}
