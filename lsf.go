package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const jobFormat = "jobid user stat queue exec_host job_name submit_time delimiter='|'"

type host struct {
	name, status, max, jobs, running, suspended, reserved string
	cpu, memory, osrel                                    string
	queues                                                []string
	resources                                             map[string]string
	attributes                                            map[string]string
	resourceKinds                                         map[string]string
	groups                                                []string
}

type job struct {
	id, user, status, queue, host, name, submitted string
}

type commandSpec struct {
	name  string
	args  []string
	stdin string
}

type commandOutput struct{ stdout, stderr string }

func runLSF(ctx context.Context, spec commandSpec) (commandOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.name, spec.args...)
	cmd.WaitDelay = time.Second
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "LC_ALL" && key != "LANG" && key != "LSB_BHOSTS_FORMAT" && key != "LSF_LSHOSTS_FORMAT" && key != "LSF_LSLOAD_FORMAT" {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "LANG=C")
	cmd.Stdin = strings.NewReader(spec.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := commandOutput{stdout.String(), stderr.String()}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		diagnostic := strings.TrimSpace(out.stderr)
		if diagnostic == "" {
			diagnostic = strings.TrimSpace(out.stdout)
		}
		return out, fmt.Errorf("%s: %w\n%s", spec.name, err, diagnostic)
	}
	return out, nil
}

var (
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	jobPattern  = regexp.MustCompile(`^[1-9][0-9]*(\[[1-9][0-9]*\])?$`)
)

func validateHost(name string) error {
	if len(name) > 253 || !hostPattern.MatchString(name) || strings.EqualFold(name, "all") {
		return fmt.Errorf("无效 host %q：只允许单个主机名，不能使用 all、选项或 user@host", name)
	}
	return nil
}

func validateJobID(id string) error {
	if !jobPattern.MatchString(id) {
		return fmt.Errorf("无效 job ID %q", id)
	}
	return nil
}

func parseHosts(text string) ([]host, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "HOST_NAME STATUS JL/U MAX NJOBS RUN SSUSP USUSP RSV" {
		return nil, errors.New("无法识别 bhosts -w 输出：需要 HOST_NAME STATUS JL/U MAX NJOBS RUN SSUSP USUSP RSV 表头")
	}
	var hosts []host
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 9 {
			return nil, fmt.Errorf("bhosts 第 %d 行格式不支持（凝聚主机组需展开为单机）", i+2)
		}
		if err := validateHost(f[0]); err != nil {
			return nil, err
		}
		h := host{name: f[0], status: f[1], max: f[3], jobs: f[4], running: f[5], suspended: f[6] + "/" + f[7], reserved: f[8]}
		for n, key := range strings.Fields("host_name status jl/u max njobs run ssusp ususp rsv") {
			h.setAttribute("batch", key, f[n])
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

func listHosts(ctx context.Context) ([]host, error) {
	return listHostsWithRequirement(ctx, "")
}

func selectHosts(ctx context.Context, requirement string) ([]host, error) {
	args := []string{"-w"}
	if requirement != "" {
		args = append(args, "-R", requirement)
	}
	out, err := runLSF(ctx, commandSpec{name: "bhosts", args: args})
	message := strings.TrimSuffix(strings.TrimSpace(out.stdout+out.stderr), ".")
	var exitErr *exec.ExitError
	if requirement != "" && (message == "No host found" || message == "No hosts found" || message == "No matching host found" || message == "No suitable host found") && (err == nil || errors.As(err, &exitErr)) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseHosts(out.stdout)
}

func listHostsWithRequirement(ctx context.Context, requirement string) ([]host, error) {
	hosts, err := selectHosts(ctx, requirement)
	if err != nil || len(hosts) == 0 {
		return hosts, err
	}
	return enrichHosts(ctx, hosts)
}

func parseJobs(text string) ([]job, error) {
	var jobs []job
	for i, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// submit_time has no delimiter; a literal "|" stays in the job name.
		f := strings.Split(line, "|")
		if len(f) < 7 {
			return nil, fmt.Errorf("bjobs 第 %d 行格式不支持：需要支持 -o 和 delimiter 的 LSF 客户端", i+1)
		}
		for n := range f {
			f[n] = strings.TrimSpace(f[n])
		}
		if err := validateJobID(f[0]); err != nil {
			return nil, err
		}
		jobs = append(jobs, job{f[0], f[1], f[2], f[3], f[4], strings.TrimSpace(strings.Join(f[5:len(f)-1], "|")), f[len(f)-1]})
	}
	return jobs, nil
}

func listJobs(ctx context.Context) ([]job, error) {
	out, err := runLSF(ctx, commandSpec{name: "bjobs", args: []string{"-a", "-noheader", "-o", jobFormat}})
	message := strings.TrimSpace(out.stdout + out.stderr)
	var exitErr *exec.ExitError
	if message == "No job found" || message == "No unfinished job found" {
		if err == nil || errors.As(err, &exitErr) {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return parseJobs(out.stdout)
}

func hostDetails(ctx context.Context, name string) (string, error) {
	if err := validateHost(name); err != nil {
		return "", err
	}
	var sections []string
	var failures []error
	for _, spec := range []commandSpec{
		{name: "bhosts", args: []string{"-l", name}},
		{name: "lshosts", args: []string{"-l", name}},
		{name: "lsload", args: []string{"-l", name}},
		{name: "lshosts", args: []string{"-s"}},
		{name: "lsload", args: []string{"-s"}},
		{name: "bqueues", args: []string{"-m", name}},
		{name: "bmgroup", args: []string{"-w", "-r"}},
	} {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		out, err := runLSF(ctx, spec)
		text := out.stdout
		if err != nil {
			failures = append(failures, err)
			text += "\n" + err.Error()
		} else if out.stderr != "" {
			text += "\n" + out.stderr
		}
		sections = append(sections, spec.name+" "+strings.Join(spec.args, " ")+"\n"+text)
	}
	return strings.Join(sections, "\n\n"), errors.Join(failures...)
}

func jobDetails(ctx context.Context, id string) (string, error) {
	if err := validateJobID(id); err != nil {
		return "", err
	}
	out, err := runLSF(ctx, commandSpec{name: "bjobs", args: []string{"-l", id}})
	return out.stdout, err
}

func jobAction(id, action string) (commandSpec, error) {
	if err := validateJobID(id); err != nil {
		return commandSpec{}, err
	}
	name := map[string]string{"x": "bkill", "u": "bstop", "v": "bresume"}[action]
	if name == "" {
		return commandSpec{}, errors.New("只支持 bkill / bstop / bresume")
	}
	return commandSpec{name: name, args: []string{id}}, nil
}

func jobPeek(id string) (commandSpec, error) {
	if err := validateJobID(id); err != nil {
		return commandSpec{}, err
	}
	return commandSpec{name: "bpeek", args: []string{id}}, nil
}

func hostAction(name, action string) (commandSpec, error) {
	if err := validateHost(name); err != nil {
		return commandSpec{}, err
	}
	if action != "hopen" && action != "hclose" {
		return commandSpec{}, errors.New("只支持 hopen / hclose")
	}
	return commandSpec{name: "badmin", args: []string{action, name}}, nil
}

func sshCommand(name string) (*exec.Cmd, error) {
	if err := validateHost(name); err != nil {
		return nil, err
	}
	return exec.Command("ssh", "--", name), nil
}

type submission struct {
	host, queue, name, output, errorFile, slots, wall, program, arguments string
}

func parseArguments(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var values []any
	if err := json.Unmarshal([]byte(text), &values); err != nil || values == nil {
		return nil, errors.New(`参数必须是 JSON 字符串数组，例如 ["--count", "10"]`)
	}
	args := make([]string, len(values))
	for i, value := range values {
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("第 %d 个参数不是字符串", i+1)
		}
		args[i] = s
	}
	return args, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (s submission) command() (commandSpec, error) {
	if err := validateHost(s.host); err != nil {
		return commandSpec{}, err
	}
	if s.program == "" || strings.HasPrefix(s.program, "-") {
		return commandSpec{}, errors.New("请填写程序路径或名称（不能以 - 开头）")
	}
	if s.queue != "" && !hostPattern.MatchString(s.queue) {
		return commandSpec{}, errors.New("queue 只能包含字母、数字、下划线、点和连字符")
	}
	args, err := parseArguments(s.arguments)
	if err != nil {
		return commandSpec{}, err
	}
	for _, value := range append([]string{s.name, s.output, s.errorFile, s.program}, args...) {
		if strings.ContainsFunc(value, unicode.IsControl) {
			return commandSpec{}, errors.New("程序、参数、名称和路径不能包含换行或控制字符")
		}
	}
	if s.slots != "" && !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(s.slots) {
		return commandSpec{}, errors.New("槽位数必须是正整数")
	}
	if s.wall != "" && !regexp.MustCompile(`^([1-9][0-9]*|[0-9]+:[0-5][0-9]|[0-9]+:[0-9]+:[0-5][0-9])$`).MatchString(s.wall) {
		return commandSpec{}, errors.New("时限用分钟或 [hour:]minute:second，例如 30 或 1:00")
	}
	spec := commandSpec{name: "bsub", args: []string{"-m", s.host}}
	for _, opt := range [][2]string{{"-q", s.queue}, {"-J", s.name}, {"-o", s.output}, {"-e", s.errorFile}, {"-n", s.slots}, {"-W", s.wall}} {
		if opt[1] == "" {
			continue
		}
		if strings.HasPrefix(opt[1], "-") {
			return commandSpec{}, fmt.Errorf("%s 的值不能以 - 开头", opt[0])
		}
		spec.args = append(spec.args, opt[0], opt[1])
	}
	// bsub reads a batch script from stdin. Quote each literal, including the
	// executable; never let LSF's execution shell reinterpret user arguments.
	quoted := []string{shellQuote(s.program)}
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	spec.stdin = "#!/bin/sh\nexec " + strings.Join(quoted, " ") + "\n"
	return spec, nil
}

func (c commandSpec) preview() string {
	parts := []string{c.name}
	for _, arg := range c.args {
		parts = append(parts, shellQuote(arg))
	}
	text := strings.Join(parts, " ")
	if c.stdin != "" {
		text += "\n\n提交脚本（stdin）:\n" + c.stdin
	}
	return text
}

func safeText(text string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(text))
}
