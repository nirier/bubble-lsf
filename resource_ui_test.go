package main

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func resourceCommands(t *testing.T) {
	t.Helper()
	fakeCommands(t, map[string]string{
		"bhosts": `if test "$2" = '-R'; then
  printf '%s' "$SELECTED_HOSTS"
else
  printf '%s' "$HOSTS_FIXTURE"
fi`,
		"bjobs":   `printf '%s' "$JOBS_FIXTURE"`,
		"lshosts": `printf '%s' "$INFO_FIXTURE"`,
		"bqueues": `printf 'QUEUE: normal\nHOSTS: all\n'`,
		"lsinfo":  `printf '%s' "$RESOURCE_FIXTURE"`,
	})
	t.Setenv("HOSTS_FIXTURE", hostFixture)
	t.Setenv("SELECTED_HOSTS", "HOST_NAME STATUS JL/U MAX NJOBS RUN SSUSP USUSP RSV\ncompute-02.example.test ok - 32 2 2 0 0 0\n")
	t.Setenv("JOBS_FIXTURE", resourceJobsFixture)
	t.Setenv("INFO_FIXTURE", hostInfoFixture)
	t.Setenv("RESOURCE_FIXTURE", resourceFixture)
}

func TestResourceEditorAndNativeQueries(t *testing.T) {
	resourceCommands(t)
	m := loadedModel(t)
	resources, _ := parseResourceCatalog(resourceFixture)
	m.Update(catalogMsg{resources: resources})
	press(m, 'R')
	if m.screen != resourceScreen || !m.resourceInput.Focused() {
		t.Fatal("R did not open the resource editor")
	}
	m.Update(tea.PasteMsg{Content: "ncp"})
	press(m, tea.KeyTab)
	if m.resourceInput.Value() != "ncpus>=" {
		t.Fatalf("typed resource completion=%q", m.resourceInput.Value())
	}
	m.resourceInput.SetValue("ncpus>=32 && osrel=='Ubuntu22.04'")
	cmd := press(m, tea.KeyEnter)
	if cmd == nil || m.resourceQueries[hostsPane] != "select[ncpus>=32 && osrel=='Ubuntu22.04']" {
		t.Fatal("resource selection was not passed to the query")
	}
	m.Update(cmd())
	if m.screen != browseScreen || len(m.table.Rows()) != 1 || m.selectedID() != "compute-02.example.test" {
		t.Fatalf("resource host query did not update the list: %+v", m.table.Rows())
	}
	cmd = press(m, 'r')
	m.Update(cmd())
	if len(m.table.Rows()) != 1 || m.selectedID() != "compute-02.example.test" {
		t.Fatal("refresh lost native selection or selected host")
	}
	press(m, 'R')
	m.resourceInput.SetValue("rusage[mem=1024]")
	if cmd := press(m, tea.KeyEnter); cmd != nil || m.screen != resourceScreen || !strings.Contains(m.status, "select") {
		t.Fatal("ignored rusage section was silently accepted as a host filter")
	}
	press(m, tea.KeyEscape)
	if m.resourceInput.Focused() || m.resourceQueries[hostsPane] == "" {
		t.Fatal("canceling native editor changed applied resource selection")
	}
	press(m, tea.KeyTab)
	m.Update(listMsg{request: m.request, pane: jobsPane})
	press(m, 'R')
	m.resourceInput.SetValue("ncpus>=32")
	cmd = press(m, tea.KeyEnter)
	m.Update(cmd())
	if len(m.table.Rows()) != 2 || m.selectedID() != "42" {
		t.Fatalf("resource jobs query=%+v", m.table.Rows())
	}
	press(m, '/')
	m.Update(tea.PasteMsg{Content: "status=RUN"})
	press(m, tea.KeyEnter)
	if len(m.table.Rows()) != 1 || m.selectedID() != "42" {
		t.Fatal("Jobs local filter was not combined with native resource selection")
	}
	press(m, 'R')
	m.resourceInput.SetValue("")
	cmd = press(m, tea.KeyEnter)
	m.Update(cmd())
	if len(m.table.Rows()) != 2 || m.resourceQueries[jobsPane] != "" || m.jobQuery != "status=RUN" || m.resourceQueries[hostsPane] == "" {
		t.Fatal("clearing Jobs native filter affected local filter or Hosts native filter")
	}
}

func TestResourceCatalogRetryAndStaleMessages(t *testing.T) {
	resourceCommands(t)
	m := loadedModel(t)
	cmd := m.loadCatalog()
	oldRequest := m.catalogRequest
	m.loadCatalog()
	m.Update(cmd())
	if !m.catalogLoading || m.catalogError != "" {
		t.Fatal("canceled old catalog query changed the current catalog")
	}
	m.Update(catalogMsg{request: oldRequest, err: errors.New("old error")})
	m.Update(catalogMsg{request: m.catalogRequest, err: errors.New("lsinfo permission denied")})
	press(m, '?')
	if m.screen != resourceHelpScreen || !strings.Contains(m.View().Content, "lsinfo permission denied") || len(m.resources) == 0 {
		t.Fatal("catalog error or fallback resource descriptions were hidden")
	}
	cmd = press(m, 'r')
	m.Update(cmd())
	if m.screen != resourceHelpScreen || m.catalogError != "" || len(m.resources) != 4 {
		t.Fatal("catalog retry failed")
	}
	press(m, tea.KeyEscape)
	press(m, 'R')
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.resourceInput.Value() != "q" || m.screen != resourceScreen {
		t.Fatal("resource editing invoked the quit shortcut")
	}
}

func TestHostJobNavigation(t *testing.T) {
	resourceCommands(t)
	m := loadedModel(t)
	if cmd := press(m, 'j'); cmd != nil || m.pane != hostsPane || m.selectedID() != "compute-02.example.test" {
		t.Fatal("j should only move down in Hosts")
	}
	if cmd := press(m, 'k'); cmd != nil || m.pane != hostsPane || m.selectedID() != "compute-01" {
		t.Fatal("k should only move up in Hosts")
	}
	cmd := press(m, tea.KeyTab)
	m.Update(cmd())
	if cmd := press(m, 'j'); cmd != nil || m.pane != jobsPane || m.selectedID() != "42" {
		t.Fatal("j should move down in Jobs")
	}
	press(m, 'k')
	if m.selectedID() != "41" {
		t.Fatal("k should move up in Jobs")
	}
	press(m, 'j')
	cmd = press(m, 'h')
	if cmd == nil || m.pane != hostsPane || !strings.Contains(m.resourceQueries[hostsPane], "hname=='compute-01' || hname=='compute-02.example.test'") {
		t.Fatal("parallel job -> execution hosts did not include all hosts")
	}
	m.Update(cmd())
	if m.screen != browseScreen || len(m.table.Rows()) != 1 {
		t.Fatal("execution host query did not return to the host list")
	}
	press(m, tea.KeyTab)
	jobs, _ := parseJobs(resourceJobsFixture)
	m.Update(listMsg{request: m.request, pane: jobsPane, jobs: jobs})
	m.jobFilters, m.jobQuery = nil, ""
	m.jobInput.SetValue("")
	m.rebuildJobs("43")
	if cmd := press(m, 'h'); cmd != nil || m.pane != jobsPane || !strings.Contains(m.status, "尚未分配") {
		t.Fatal("pending job navigated to an invented execution host")
	}
}

func TestResourceViewsFitSupportedTerminals(t *testing.T) {
	for _, size := range [][2]int{{76, 22}, {80, 24}, {120, 40}} {
		m := loadedModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		resources, _ := parseResourceCatalog(resourceFixture)
		m.Update(catalogMsg{resources: resources})
		check := func() {
			t.Helper()
			view := m.View()
			if lipgloss.Width(view.Content) > size[0] || lipgloss.Height(view.Content) > size[1] {
				t.Fatalf("screen=%v pane=%v size=%v actual=%dx%d\n%s", m.screen, m.pane, size, lipgloss.Width(view.Content), lipgloss.Height(view.Content), view.Content)
			}
		}
		press(m, 'R')
		m.resourceInput.SetValue("select[ncpus>=16 && osrel=='Ubuntu22.04']")
		check()
		m.resourceInput.SetValue("")
		check()
		press(m, tea.KeyEscape)
		press(m, '?')
		check()
		press(m, tea.KeyEscape)
		press(m, tea.KeyTab)
		jobs, _ := parseJobs(resourceJobsFixture)
		m.Update(listMsg{request: m.request, pane: jobsPane, jobs: jobs})
		check()
		press(m, '/')
		check()
		m.jobInput.SetValue("queue:")
		m.jobInput.CursorEnd()
		check()
		press(m, tea.KeyEscape)
		press(m, 'R')
		check()
	}
}
