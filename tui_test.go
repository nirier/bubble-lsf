package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func press(m *model, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func loadedModel(t *testing.T) *model {
	t.Helper()
	m := newModel(context.Background())
	hosts, err := parseHosts(hostFixture)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(listMsg{pane: hostsPane, hosts: hosts})
	return m
}

func TestRefreshPreservesSelectionAndIgnoresOldResults(t *testing.T) {
	m := loadedModel(t)
	m.table.SetCursor(1)
	m.refresh()
	oldRequest := m.request
	m.refresh() // A second refresh while the table is empty must retain selection.
	m.Update(listMsg{request: oldRequest, pane: hostsPane, err: errors.New("old failure")})
	if !m.loading || m.screen != browseScreen {
		t.Fatal("stale response changed the active query")
	}
	hosts, _ := parseHosts(hostFixture)
	m.Update(listMsg{request: m.request, pane: hostsPane, hosts: hosts})
	if m.selectedID() != "compute-02.example.test" || m.loading {
		t.Fatalf("selection=%q loading=%v", m.selectedID(), m.loading)
	}
}

func TestQueryFailureCanRetry(t *testing.T) {
	fakeCommands(t, map[string]string{"bhosts": "printf '%s' '" + hostFixture + "'"})
	m := newModel(context.Background())
	m.refresh()
	m.Update(listMsg{request: m.request, pane: hostsPane, err: errors.New("permission denied")})
	if m.screen != detailScreen || !strings.Contains(m.View().Content, "permission denied") {
		t.Fatal("query failure was hidden")
	}
	cmd := press(m, 'r')
	if cmd == nil {
		t.Fatal("r did not retry the failed query")
	}
	m.Update(cmd())
	if m.screen != browseScreen || m.failed || len(m.table.Rows()) != 2 {
		t.Fatalf("retry did not restore list: %+v", m)
	}
}

func TestDetailRetryAndCancellation(t *testing.T) {
	fakeCommands(t, map[string]string{
		"bhosts": "printf 'host detail'", "lshosts": "printf 'machine detail'", "lsload": "printf 'load detail'",
	})
	m := loadedModel(t)
	press(m, tea.KeyEnter)
	oldRequest := m.request
	cmd := press(m, 'r')
	if cmd == nil || m.request == oldRequest {
		t.Fatal("detail retry did not start a new query")
	}
	m.Update(cmd())
	if !strings.Contains(m.viewport.GetContent(), "machine detail") || m.loading {
		t.Fatal("detail result not rendered")
	}
	press(m, tea.KeyEscape)
	m.Update(detailMsg{request: m.request - 1, text: "stale detail"})
	if m.screen != browseScreen || strings.Contains(m.View().Content, "stale detail") || strings.Contains(m.status, "读取中") {
		t.Fatal("returning from detail left stale content or loading status")
	}
}

func TestPaneSwitchCancelsOldQuery(t *testing.T) {
	m := loadedModel(t)
	ctx, oldRequest := m.beginQuery()
	press(m, tea.KeyTab)
	if ctx.Err() != context.Canceled || m.pane != jobsPane {
		t.Fatal("switching panes did not cancel host query")
	}
	m.Update(listMsg{request: oldRequest, pane: hostsPane, err: errors.New("stale host failure")})
	m.Update(listMsg{request: m.request, pane: jobsPane, jobs: []job{{id: "42", name: "test job"}}})
	if m.selectedID() != "42" || m.failed {
		t.Fatal("host response overwrote job list")
	}
}

func TestSubmissionPreviewCancellationAndSingleExecution(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls")
	t.Setenv("CALLS_FILE", logFile)
	fakeCommands(t, map[string]string{
		"bsub":   "printf 'submit\\n' >> \"$CALLS_FILE\"\n/bin/cat >/dev/null\nprintf 'Job <42> is submitted.'",
		"bhosts": "printf '%s' '" + hostFixture + "'",
	})
	m := loadedModel(t)
	press(m, 'n')
	m.Update(tea.PasteMsg{Content: "/usr/bin/hostname"})
	m.reviewForm()
	if m.screen != confirmScreen || !strings.Contains(m.View().Content, "compute-01") {
		t.Fatal("submission did not present target for review")
	}
	press(m, tea.KeyEscape)
	if m.screen != formScreen || m.pending != nil || m.inputs[7].Value() != "/usr/bin/hostname" || !m.inputs[7].Focused() {
		t.Fatal("canceling preview lost the editable form")
	}
	if _, err := os.Stat(logFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview or cancellation executed bsub")
	}
	m.reviewForm()
	cmd := press(m, 'y')
	if cmd == nil || m.screen != workingScreen || m.pending != nil {
		t.Fatal("confirmation did not start exactly one action")
	}
	if repeat := press(m, 'y'); repeat != nil {
		t.Fatal("second confirmation started another action")
	}
	m.Update(cmd())
	if !strings.Contains(m.View().Content, "Job <42>") {
		t.Fatal("submission result not visible")
	}
	// Refreshing an action result must query the list, never repeat the action.
	cmd = press(m, 'r')
	if cmd == nil {
		t.Fatal("action result did not allow list refresh")
	}
	m.Update(cmd())
	calls, err := os.ReadFile(logFile)
	if err != nil || string(calls) != "submit\n" || m.screen != browseScreen {
		t.Fatalf("calls=%q err=%v screen=%v", calls, err, m.screen)
	}
}

func TestHostActionRequiresConfirmation(t *testing.T) {
	m := loadedModel(t)
	press(m, 'c')
	if m.screen != confirmScreen || m.pending == nil || m.pending.args[1] != "compute-01" {
		t.Fatal("host action bypassed confirmation or selected wrong target")
	}
	if !strings.Contains(m.View().Content, "不是关机") {
		t.Fatal("close operation was not explained")
	}
	press(m, 'n')
	if m.screen != browseScreen || m.pending != nil {
		t.Fatal("cancel did not discard the action")
	}
	press(m, 'y')
	if m.screen != browseScreen {
		t.Fatal("canceled action remained executable")
	}
}

func TestJobActionConfirmsOnceAndPeekDoesNotConfirm(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls")
	t.Setenv("CALLS_FILE", logFile)
	fakeCommands(t, map[string]string{
		"bkill": "printf 'kill\n' >> \"$CALLS_FILE\"\nprintf 'Job <42> is being terminated'",
		"bpeek": "printf 'peek\n' >> \"$CALLS_FILE\"\nprintf 'job output'",
	})
	m := newModel(context.Background())
	m.Update(tea.KeyPressMsg{Code: '2'})
	m.Update(listMsg{request: m.request, pane: jobsPane, jobs: []job{{id: "42", status: "RUN", name: "test"}}})
	press(m, 'x')
	if m.screen != confirmScreen || m.pending == nil || m.pending.name != "bkill" || m.pending.args[0] != "42" {
		t.Fatal("bkill bypassed confirmation")
	}
	press(m, 'n')
	if m.pending != nil {
		t.Fatal("cancel kept the kill")
	}
	if _, err := os.Stat(logFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel executed bkill")
	}
	press(m, 'x')
	cmd := press(m, 'y')
	if cmd == nil || m.screen != workingScreen {
		t.Fatal("confirm did not run once")
	}
	if press(m, 'y') != nil {
		t.Fatal("second y repeated bkill")
	}
	m.Update(cmd())
	m.pane = jobsPane
	m.detailTarget = "42"
	m.screen = detailScreen
	cmd = press(m, 'e')
	if cmd == nil || m.screen != workingScreen || m.pending != nil {
		t.Fatal("bpeek did not run as a read")
	}
	m.Update(cmd())
	calls, err := os.ReadFile(logFile)
	if err != nil || string(calls) != "kill\npeek\n" || !strings.Contains(m.View().Content, "job output") {
		t.Fatalf("calls=%q err=%v", calls, err)
	}
}

func TestSSHJobDirUsesCWDAndFirstHost(t *testing.T) {
	fakeCommands(t, map[string]string{
		"bjobs": "printf '%s\\n' 'Job <42>' 'Started on compute-02.example.test' 'CWD </home/user/src>'",
	})
	m := newModel(context.Background())
	m.Update(tea.KeyPressMsg{Code: '2'})
	m.Update(listMsg{request: m.request, pane: jobsPane, jobs: []job{{id: "42", status: "RUN", host: "compute-01"}}})
	cmd := press(m, 's')
	if cmd == nil || m.screen != workingScreen {
		t.Fatal("s did not look up the job directory")
	}
	msg := cmd().(jobDirMsg)
	if msg.err != nil || msg.host != "compute-02.example.test" || msg.dir != "/home/user/src" || msg.request != m.request {
		t.Fatalf("lookup %+v", msg)
	}
	m.Update(msg)
	if m.screen != browseScreen {
		t.Fatal("successful lookup did not hand off to ssh")
	}
	m.Update(jobDirMsg{request: m.request - 1, err: errors.New("older")})
	if m.screen != browseScreen {
		t.Fatal("stale directory result changed the screen")
	}
}

func TestSmallTerminalBlocksConfirmation(t *testing.T) {
	m := loadedModel(t)
	press(m, 'c')
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if cmd := press(m, 'y'); cmd != nil || m.screen != confirmScreen {
		t.Fatal("action executed while preview was hidden")
	}
	if !strings.Contains(m.View().Content, "76×22") {
		t.Fatal("small terminal guidance not shown")
	}
	press(m, tea.KeyEscape)
	if m.pending != nil || m.screen != browseScreen {
		t.Fatal("small terminal prevented cancellation")
	}
}

func TestViewsFitSupportedTerminal(t *testing.T) {
	for _, size := range [][2]int{{76, 22}, {80, 24}, {120, 40}} {
		m := loadedModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		check := func() {
			t.Helper()
			view := m.View()
			if !view.AltScreen || lipgloss.Width(view.Content) > size[0] || lipgloss.Height(view.Content) > size[1] {
				for _, line := range strings.Split(view.Content, "\n") {
					if lipgloss.Width(line) > size[0] {
						t.Logf("overflowing line: %q", line)
					}
				}
				t.Fatalf("screen %v overflows %dx%d: %dx%d", m.screen, size[0], size[1], lipgloss.Width(view.Content), lipgloss.Height(view.Content))
			}
		}
		check()
		press(m, '/')
		m.Update(tea.PasteMsg{Content: "cpu>=16 memory>=64G osrel:7 queue:normal"})
		check()
		press(m, tea.KeyEscape)
		press(m, tea.KeyRight)
		check()
		press(m, tea.KeyEnter)
		m.Update(detailMsg{request: m.request, text: strings.Repeat("中文 long detail ", 200)})
		check()
		press(m, tea.KeyEscape)
		press(m, 'n')
		check()
		m.inputs[4].SetValue("hostname")
		m.reviewForm()
		check()
	}
}

func TestHostFilteringSelectionAndRefresh(t *testing.T) {
	m := loadedModel(t)
	hosts, _ := parseHosts(hostFixture)
	hosts[0].cpu, hosts[0].memory, hosts[0].osrel, hosts[0].queues = "16", "64G", "7.9", []string{"normal"}
	hosts[1].cpu, hosts[1].memory, hosts[1].osrel, hosts[1].queues = "32", "128G", "8.6", []string{"batch"}
	m.Update(listMsg{pane: hostsPane, hosts: hosts})
	press(m, '/')
	m.Update(tea.PasteMsg{Content: "cpu>=32 memory>=128G osrel:8 queue:batch"})
	press(m, tea.KeyEnter)
	if m.filtering || len(m.table.Rows()) != 1 || m.selectedID() != hosts[1].name || m.table.Rows()[0][1] != "32" {
		t.Fatalf("filter did not select the matching host: %+v", m.table.Rows())
	}
	press(m, 'c')
	if m.pending == nil || m.pending.args[1] != hosts[1].name {
		t.Fatal("management action targeted a host hidden by the filter")
	}
	press(m, tea.KeyEscape)
	m.refresh()
	m.Update(listMsg{request: m.request, pane: hostsPane, hosts: hosts})
	if len(m.table.Rows()) != 1 || m.selectedID() != hosts[1].name {
		t.Fatal("refresh lost the filter or selected host")
	}
	press(m, tea.KeyTab)
	m.Update(listMsg{request: m.request, pane: jobsPane, jobs: []job{{id: "42", name: "job"}}})
	if len(m.table.Rows()) != 1 || m.selectedID() != "42" {
		t.Fatal("host filter affected Jobs")
	}
	press(m, tea.KeyTab)
	m.Update(listMsg{request: m.request, pane: hostsPane, hosts: hosts})
	if len(m.table.Rows()) != 1 || m.filterQuery == "" {
		t.Fatal("pane switching lost host filter")
	}
	press(m, '/')
	m.filterInput.SetValue("")
	press(m, tea.KeyEnter)
	if len(m.table.Rows()) != 2 || m.selectedID() != hosts[1].name {
		t.Fatal("clearing filter did not restore all hosts and preserve selection")
	}
}

func TestHostFilterEditingAndInvalidQueries(t *testing.T) {
	m := loadedModel(t)
	press(m, '/')
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !m.filtering || m.filterInput.Value() != "q" || m.screen != browseScreen {
		t.Fatal("filter input triggered the quit shortcut")
	}
	press(m, tea.KeyEscape)
	if m.filterInput.Value() != "" || m.filtering || len(m.table.Rows()) != 2 {
		t.Fatal("canceling filter editing changed the list")
	}
	press(m, '/')
	m.filterInput.SetValue("memory>=bogus")
	press(m, tea.KeyEnter)
	if !m.filtering || m.filterQuery != "" || len(m.table.Rows()) != 2 || !strings.Contains(m.status, "有效数值") {
		t.Fatal("invalid filter silently hid hosts or replaced the previous filter")
	}
	m.filterInput.SetValue("host:no-match")
	press(m, tea.KeyEnter)
	if len(m.table.Rows()) != 0 || !strings.Contains(m.View().Content, "没有匹配的主机") {
		t.Fatal("no-match feedback missing")
	}
	for _, key := range []rune{'c', 'o', 's', 'n', tea.KeyEnter} {
		if cmd := press(m, key); cmd != nil || m.screen != browseScreen {
			t.Fatal("empty filtered list allowed an action on a stale host")
		}
	}
}

func TestPartialHostListRemainsUsable(t *testing.T) {
	m := loadedModel(t)
	hosts, _ := parseHosts(hostFixture)
	m.Update(listMsg{pane: hostsPane, hosts: hosts, err: errors.New("bqueues: permission denied")})
	if m.failed || m.screen != browseScreen || len(m.table.Rows()) != 2 || !strings.Contains(m.status, "permission denied") {
		t.Fatal("optional information failure hid the host list or diagnostic")
	}
}
