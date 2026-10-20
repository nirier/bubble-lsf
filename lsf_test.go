package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const hostFixture = `HOST_NAME STATUS JL/U MAX NJOBS RUN SSUSP USUSP RSV
compute-01 ok - 16 3 2 1 0 0
compute-02.example.test closed_Full - - 0 0 0 0 0
`

func fakeCommands(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Never fall through to a developer's actual LSF installation.
	t.Setenv("PATH", dir)
}

func TestParseHosts(t *testing.T) {
	hosts, err := parseHosts(hostFixture)
	if err != nil || len(hosts) != 2 {
		t.Fatalf("hosts=%v, err=%v", hosts, err)
	}
	if hosts[0].name != "compute-01" || hosts[0].suspended != "1/0" || hosts[1].max != "-" {
		t.Fatalf("incorrect columns: %+v", hosts)
	}
	for _, text := range []string{"", "unexpected output", "HOST_NAME STATUS JL/U MAX NJOBS RUN SSUSP USUSP RSV\ngroup ok 2/1/0/0", strings.Replace(hostFixture, "compute-01", "-bad", 1)} {
		if _, err := parseHosts(text); err == nil {
			t.Errorf("accepted malformed hosts: %q", text)
		}
	}
	header, _, _ := strings.Cut(hostFixture, "\n")
	if hosts, err := parseHosts(header); err != nil || len(hosts) != 0 {
		t.Fatalf("empty table: %v, %v", hosts, err)
	}
}

func TestParseJobs(t *testing.T) {
	jobs, err := parseJobs("42 | user | RUN | normal | compute-01 | a job | with pipes | Oct 8 10:00\n43[2]|user|PEND|normal|-|array task|Oct 8 10:01\n")
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs=%v, err=%v", jobs, err)
	}
	if jobs[0].name != "a job|with pipes" || jobs[0].submitted != "Oct 8 10:00" || jobs[1].id != "43[2]" {
		t.Fatalf("lost name or array index: %+v", jobs)
	}
	for _, text := range []string{"unexpected header", "0|u|RUN|q|h|name|t", "42;bad|u|RUN|q|h|name|t", "42|u|RUN|q|h|name"} {
		if _, err := parseJobs(text); err == nil {
			t.Errorf("accepted malformed jobs: %q", text)
		}
	}
}

func TestListJobsEmptyVersusFailure(t *testing.T) {
	fakeCommands(t, map[string]string{"bjobs": `printf '%s' "$FAKE_OUT"
printf '%s' "$FAKE_ERR" >&2
exit "$FAKE_CODE"`})
	for _, tc := range []struct {
		name, stdout, stderr, code string
		wantErr                    bool
	}{
		{"empty success", "", "", "0", false},
		{"no jobs stderr", "", "No job found\n", "255", false},
		{"no jobs stdout", "No unfinished job found\n", "", "1", false},
		{"authentication", "", "User permission denied", "1", true},
		{"not just no jobs", "", "Authentication failed\nNo job found\n", "1", true},
		{"unsupported format", "", "Unknown option -o", "2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAKE_OUT", tc.stdout)
			t.Setenv("FAKE_ERR", tc.stderr)
			t.Setenv("FAKE_CODE", tc.code)
			jobs, err := listJobs(context.Background())
			if (err != nil) != tc.wantErr || len(jobs) != 0 {
				t.Fatalf("jobs=%v err=%v, wantErr=%v", jobs, err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), strings.TrimSpace(tc.stderr)) {
				t.Fatalf("stderr lost: %v", err)
			}
		})
	}
}

func TestTargets(t *testing.T) {
	for _, name := range []string{"-oProxyCommand=bad", "all", "ALL", "user@host", "host;bad", "host other", "host\nother", ""} {
		if _, err := hostAction(name, "hclose"); err == nil {
			t.Errorf("accepted admin target %q", name)
		}
		if _, err := sshCommand(name); err == nil {
			t.Errorf("accepted SSH target %q", name)
		}
	}
	if _, err := hostAction("compute-01", "shutdown"); err == nil {
		t.Fatal("accepted unimplemented management action")
	}
	for _, id := range []string{"0", "42;bad", "all", "-k", ""} {
		if _, err := jobAction(id, "x"); err == nil {
			t.Errorf("accepted job action target %q", id)
		}
		if _, err := jobPeek(id); err == nil {
			t.Errorf("accepted peek target %q", id)
		}
	}
	if _, err := jobAction("42", "bmod"); err == nil {
		t.Fatal("accepted unimplemented job action")
	}
	kill, err := jobAction("43[2]", "x")
	if err != nil || kill.name != "bkill" || kill.args[0] != "43[2]" {
		t.Fatalf("job action %+v %v", kill, err)
	}
	cmd, err := sshCommand("compute-01.example.test")
	if err != nil || !reflect.DeepEqual(cmd.Args, []string{"ssh", "--", "compute-01.example.test"}) {
		t.Fatalf("SSH command=%v err=%v", cmd, err)
	}
}

func TestSubmitLiteralArguments(t *testing.T) {
	args := []string{"<%s>\\n", "", "a b", "a'b", "$(printf injected)", "`printf injected`", "*", "$HOME", "中文", "#BSUB -q other"}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	s := submission{host: "compute-01", queue: "normal", name: "literal job", output: "logs/job.%J.out", errorFile: "logs/job.%J.err", slots: "4", wall: "1:00", program: "/usr/bin/printf", arguments: string(encoded)}
	spec, err := s.command()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-m", "compute-01", "-q", "normal", "-J", "literal job", "-o", "logs/job.%J.out", "-e", "logs/job.%J.err", "-n", "4", "-W", "1:00"}
	if spec.name != "bsub" || !reflect.DeepEqual(spec.args, want) {
		t.Fatalf("incorrect bsub options: %+v", spec)
	}
	// Execute only the generated script with printf, never bsub or a real job.
	cmd := exec.Command("/bin/sh")
	cmd.Stdin = strings.NewReader(spec.stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	var expected strings.Builder
	for _, arg := range args[1:] {
		expected.WriteString("<" + arg + ">\n")
	}
	if string(out) != expected.String() {
		t.Fatalf("arguments changed: got %q, want %q", out, expected.String())
	}
}

func TestSubmitValidation(t *testing.T) {
	for _, raw := range []string{"null", `[null]`, `[1]`, `{"x":1}`, `"argument"`, `["a",]`} {
		if _, err := parseArguments(raw); err == nil {
			t.Errorf("accepted invalid argument array %q", raw)
		}
	}
	for _, s := range []submission{
		{host: "all", program: "hostname"},
		{host: "compute-01", program: "-option"},
		{host: "compute-01", program: ""},
		{host: "compute-01", program: "hostname\n#BSUB -q other"},
		{host: "compute-01", program: "hostname", arguments: `["line\nbreak"]`},
		{host: "compute-01", program: "hostname", arguments: `["\u0000"]`},
		{host: "compute-01", program: "hostname", queue: "-q"},
		{host: "compute-01", program: "hostname", output: "-bad"},
		{host: "compute-01", program: "hostname", slots: "0"},
		{host: "compute-01", program: "hostname", slots: "4,8"},
		{host: "compute-01", program: "hostname", wall: "1:60"},
		{host: "compute-01", program: "hostname", wall: "-30"},
	} {
		if _, err := s.command(); err == nil {
			t.Errorf("accepted unsafe submission: %+v", s)
		}
	}
}

func TestSubmitCommandIO(t *testing.T) {
	dir := t.TempDir()
	argsFile, inputFile := filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	t.Setenv("ARGS_FILE", argsFile)
	t.Setenv("INPUT_FILE", inputFile)
	fakeCommands(t, map[string]string{"bsub": `printf '%s\n' "$@" > "$ARGS_FILE"
/bin/cat > "$INPUT_FILE"
printf 'Job <42> is submitted to queue <normal>.\n'`})
	spec, err := (submission{host: "compute-01", program: "hostname"}).command()
	if err != nil {
		t.Fatal(err)
	}
	out, err := runLSF(context.Background(), spec)
	if err != nil || !strings.Contains(out.stdout, "Job <42>") {
		t.Fatalf("out=%+v, err=%v", out, err)
	}
	gotArgs, err := os.ReadFile(argsFile)
	if err != nil || string(gotArgs) != "-m\ncompute-01\n" {
		t.Fatalf("args=%q err=%v", gotArgs, err)
	}
	gotInput, err := os.ReadFile(inputFile)
	if err != nil || string(gotInput) != spec.stdin {
		t.Fatalf("stdin=%q err=%v", gotInput, err)
	}
}

func TestCommandEnvironmentAndTimeout(t *testing.T) {
	fakeCommands(t, map[string]string{
		"bhosts": `printf '%s|%s|%s' "$LC_ALL" "$LANG" "${LSB_BHOSTS_FORMAT-unset}"`,
		"bjobs":  `exec /bin/sleep 5`,
	})
	t.Setenv("LC_ALL", "custom")
	t.Setenv("LANG", "custom")
	t.Setenv("LSB_BHOSTS_FORMAT", "custom")
	out, err := runLSF(context.Background(), commandSpec{name: "bhosts"})
	if err != nil || out.stdout != "C|C|unset" {
		t.Fatalf("environment=%+v err=%v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = runLSF(ctx, commandSpec{name: "bjobs"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout not enforced: %v after %v", err, time.Since(start))
	}
}

func TestPartialHostDetails(t *testing.T) {
	fakeCommands(t, map[string]string{
		"bhosts":  `printf 'HOST compute-01\n'`,
		"lshosts": `printf 'permission denied' >&2; exit 1`,
		"lsload":  `printf 'r15s 0.1\n'`,
	})
	text, err := hostDetails(context.Background(), "compute-01")
	if err == nil || !strings.Contains(text, "HOST compute-01") || !strings.Contains(text, "permission denied") || !strings.Contains(text, "r15s 0.1") {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestCommandFailureKeepsStdoutDiagnostic(t *testing.T) {
	fakeCommands(t, map[string]string{"bhosts": "printf 'LSF daemon unavailable'; exit 1"})
	_, err := listHosts(context.Background())
	if err == nil || !strings.Contains(err.Error(), "LSF daemon unavailable") {
		t.Fatalf("stdout diagnostic lost: %v", err)
	}
}

func TestSafeText(t *testing.T) {
	text := "\x1b[31mred\x1b[0m\x1b]52;c;dGVzdA==\a\x00\nnext\tcolumn"
	if got := safeText(text); got != "red\nnext\tcolumn" {
		t.Fatalf("unsafe terminal text: %q", got)
	}
}
