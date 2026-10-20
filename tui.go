package main

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type screen int

const (
	browseScreen screen = iota
	detailScreen
	formScreen
	confirmScreen
	workingScreen
	resourceScreen
	resourceHelpScreen
)

type pane int

const (
	hostsPane pane = iota
	jobsPane
)

type listMsg struct {
	request int
	pane    pane
	hosts   []host
	jobs    []job
	err     error
}

type detailMsg struct {
	request int
	text    string
	err     error
}

type actionMsg struct {
	out commandOutput
	err error
}

type sshMsg struct{ err error }

type model struct {
	ctx             context.Context
	cancelQuery     context.CancelFunc
	request         int
	pane            pane
	screen          screen
	returnTo        screen
	width           int
	height          int
	loading         bool
	failed          bool
	table           table.Model
	viewport        viewport.Model
	title           string
	detailTarget    string
	status          string
	selected        string
	pending         *commandSpec
	inputs          []textinput.Model
	focus           int
	hosts           []host
	hostWarning     string
	filterInput     textinput.Model
	filterQuery     string
	filters         []hostPredicate
	filtering       bool
	hostScroll      int
	hostColumns     []string
	jobs            []job
	jobInput        textinput.Model
	jobQuery        string
	jobFilters      []jobPredicate
	resourceInput   textinput.Model
	resourceQueries [2]string
	resources       []resourceDescriptor
	catalogError    string
	catalogRequest  int
	catalogCancel   context.CancelFunc
	catalogLoading  bool
	completionIndex int
}

var accent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))

func newModel(ctx context.Context) *model {
	t := table.New(table.WithFocused(true), table.WithHeight(12))
	styles := table.DefaultStyles()
	styles.Selected = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
	t.SetStyles(styles)
	v := viewport.New(viewport.WithWidth(76), viewport.WithHeight(12))
	v.SoftWrap = true
	m := &model{ctx: ctx, width: 80, height: 24, table: t, viewport: v}
	m.filterInput = textinput.New()
	m.filterInput.Prompt = "过滤: "
	m.filterInput.Placeholder = "cpu>=16 mem>=32G osrel:7 ssd=1"
	m.filterInput.CharLimit = 1024
	m.filterInput.SetVirtualCursor(true)
	m.jobInput = textinput.New()
	m.jobInput.Prompt = "过滤: "
	m.jobInput.Placeholder = "status=RUN queue:normal host:compute"
	m.jobInput.CharLimit = 1024
	m.jobInput.SetVirtualCursor(true)
	m.resourceInput = textinput.New()
	m.resourceInput.Prompt = "R: "
	m.resourceInput.Placeholder = "select[type==any && ncpus>=16]"
	m.resourceInput.CharLimit = 4096
	m.resourceInput.SetVirtualCursor(true)
	m.resources = builtinResources()
	m.resize()
	return m
}

func (m *model) Init() tea.Cmd { return tea.Batch(m.refresh(), m.loadCatalog()) }

func (m *model) beginQuery() (context.Context, int) {
	m.stopQuery()
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelQuery = cancel
	m.loading = true
	return ctx, m.request
}

func (m *model) stopQuery() {
	if m.cancelQuery != nil {
		m.cancelQuery()
		m.cancelQuery = nil
	}
	m.request++
	m.loading = false
}

func (m *model) refresh() tea.Cmd {
	if id := m.selectedID(); id != "" || !m.loading {
		m.selected = id
	}
	ctx, request := m.beginQuery()
	p := m.pane
	requirement := m.resourceQueries[p]
	m.screen, m.failed = browseScreen, false
	m.detailTarget = ""
	m.status = "读取中…"
	m.table.SetRows(nil)
	return func() tea.Msg {
		result := listMsg{request: request, pane: p}
		if p == hostsPane {
			result.hosts, result.err = listHostsWithRequirement(ctx, requirement)
		} else {
			result.jobs, result.err = listJobsWithRequirement(ctx, requirement)
		}
		return result
	}
}

func (m *model) selectedID() string {
	row := m.table.SelectedRow()
	if len(row) == 0 {
		return ""
	}
	return row[0]
}

func (m *model) resize() {
	width := max(20, m.width-4)
	height := max(3, m.height-10)
	m.table.SetWidth(width)
	m.table.SetHeight(height)
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(height)
	var columns []table.Column
	if m.pane == hostsPane {
		columns = []table.Column{
			{Title: "HOST", Width: max(18, width-106)}, {Title: "CPU", Width: 4},
			{Title: "MEMORY", Width: 8}, {Title: "OSREL", Width: 14}, {Title: "QUEUES", Width: 22},
			{Title: "STATUS", Width: 12},
			{Title: "MAX", Width: 5}, {Title: "JOBS", Width: 5}, {Title: "RUN", Width: 5},
			{Title: "S/U", Width: 5}, {Title: "RSV", Width: 4},
		}
		m.hostColumns = hostTableFields(m.hosts)
		for _, key := range m.hostColumns {
			colWidth := max(10, ansi.StringWidth(key))
			for _, h := range m.hosts {
				value, _ := hostField(h, key)
				colWidth = max(colWidth, min(28, ansi.StringWidth(safeText(value))))
			}
			columns = append(columns, table.Column{Title: key, Width: colWidth})
		}
	} else {
		columns = []table.Column{
			{Title: "JOB ID", Width: 13}, {Title: "USER", Width: 8}, {Title: "STAT", Width: 6},
			{Title: "QUEUE", Width: 9}, {Title: "EXEC HOST", Width: 14}, {Title: "SUBMIT", Width: 16},
			{Title: "NAME", Width: max(8, width-80)},
		}
	}
	m.table.SetColumns(columns)
	if m.pane == hostsPane {
		tableWidth := 0
		for _, col := range columns {
			tableWidth += col.Width + 2
		}
		m.table.SetWidth(tableWidth)
		m.hostScroll = min(m.hostScroll, max(0, tableWidth-width))
	}
	m.table.SetHeight(max(2, height-1))
	m.filterInput.SetWidth(max(10, width-7))
	m.jobInput.SetWidth(max(10, width-7))
	m.resourceInput.SetWidth(max(10, width-4))
	for i := range m.inputs {
		m.inputs[i].SetWidth(max(10, width-17))
	}
}

func (m *model) showText(title, text string) {
	m.title = title
	m.viewport.SetContent(safeText(text))
	m.viewport.GotoTop()
}

func (m *model) openDetails() tea.Cmd {
	return m.loadDetails(m.selectedID())
}

func (m *model) loadDetails(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	ctx, request := m.beginQuery()
	p := m.pane
	m.screen = detailScreen
	m.detailTarget = id
	m.status = "读取中… · Esc 返回列表"
	m.showText(id, "读取中…")
	var summary string
	if p == hostsPane {
		for _, h := range m.hosts {
			if h.name == id {
				summary = hostSnapshot(h)
				break
			}
		}
	}
	return func() tea.Msg {
		var text string
		var err error
		if p == hostsPane {
			text, err = hostDetails(ctx, id)
		} else {
			text, err = jobDetails(ctx, id)
		}
		return detailMsg{request, summary + text, err}
	}
}

func hostValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func (m *model) rebuildHosts(selected string) {
	m.table.SetRows(nil)
	m.resize()
	var rows []table.Row
	for _, h := range m.hosts {
		if !matchesHost(h, m.filters) {
			continue
		}
		row := table.Row{h.name, hostValue(h.cpu), hostValue(h.memory), hostValue(h.osrel), hostValue(strings.Join(h.queues, ",")), h.status, h.max, h.jobs, h.running, h.suspended, h.reserved}
		for _, key := range m.hostColumns {
			value, _ := hostField(h, key)
			row = append(row, hostValue(value))
		}
		for i := range row {
			row[i] = safeText(row[i])
		}
		rows = append(rows, row)
	}
	m.table.SetRows(rows)
	m.table.SetCursor(0)
	for i, row := range rows {
		if row[0] == selected {
			m.table.SetCursor(i)
			break
		}
	}
	m.status = m.listStatus()
}

func (m *model) updateFilter(msg tea.Msg) tea.Cmd {
	input := m.activeFilterInput()
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "tab", "up", "down":
			m.completeKey(key.String(), input, false)
			return nil
		case "esc":
			query := m.filterQuery
			if m.pane == jobsPane {
				query = m.jobQuery
			}
			input.SetValue(query)
			input.Blur()
			m.filtering = false
			m.status = m.listStatus()
			return nil
		case "enter":
			if m.pane == jobsPane {
				predicates, err := compileJobFilter(input.Value())
				if err != nil {
					m.status = err.Error()
					return nil
				}
				selected := m.selectedID()
				m.jobFilters, m.jobQuery = predicates, input.Value()
				input.Blur()
				m.filtering = false
				if !m.loading {
					m.rebuildJobs(selected)
				}
				return nil
			}
			predicates, err := compileHostFilter(m.filterInput.Value(), m.filterHosts()...)
			if err != nil {
				m.status = err.Error()
				return nil
			}
			selected := m.selectedID()
			m.filters, m.filterQuery = predicates, m.filterInput.Value()
			m.filterInput.Blur()
			m.filtering = false
			if !m.loading {
				m.rebuildHosts(selected)
			}
			return nil
		}
	}
	var cmd tea.Cmd
	*input, cmd = input.Update(msg)
	m.completionIndex = 0
	return cmd
}

func (m *model) runOnce(spec commandSpec, title string) tea.Cmd {
	m.stopQuery()
	m.screen = workingScreen
	m.title = title
	m.status = "等待命令返回（超时 15 秒）；期间暂停快捷键"
	ctx := m.ctx
	return func() tea.Msg {
		out, err := runLSF(ctx, spec)
		return actionMsg{out, err}
	}
}

func (m *model) confirm(spec commandSpec, title string, returnTo screen) {
	m.stopQuery()
	m.pending = &spec
	m.detailTarget = ""
	m.screen, m.returnTo = confirmScreen, returnTo
	m.status = "尚未执行；确认前请核对目标与全部参数"
	m.showText(title, spec.preview())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case catalogMsg:
		if msg.request != m.catalogRequest {
			return m, nil
		}
		m.catalogLoading = false
		if msg.err != nil {
			m.catalogError = msg.err.Error()
		} else {
			m.resources, m.catalogError = msg.resources, ""
		}
		if m.screen == resourceHelpScreen {
			m.showCatalog()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case listMsg:
		if msg.request != m.request || msg.pane != m.pane || m.screen != browseScreen {
			return m, nil
		}
		m.loading = false
		if msg.err != nil && (m.pane != hostsPane || len(msg.hosts) == 0) {
			m.filtering = false
			m.activeFilterInput().Blur()
			m.failed = true
			m.status = "查询失败；Esc 返回，r 重试"
			m.screen = detailScreen
			m.showText("查询失败", msg.err.Error())
			return m, nil
		}
		if m.pane == hostsPane {
			m.hosts, m.hostWarning = msg.hosts, ""
			if msg.err != nil {
				m.hostWarning = "部分信息读取失败: " + msg.err.Error()
			}
			m.rebuildHosts(m.selected)
			return m, nil
		}
		m.jobs = msg.jobs
		m.rebuildJobs(m.selected)
		return m, nil
	case detailMsg:
		if msg.request != m.request || m.screen != detailScreen {
			return m, nil
		}
		m.loading = false
		text := msg.text
		if msg.err != nil {
			text += "\n\n读取失败（已有部分结果如上）:\n" + msg.err.Error()
		}
		if strings.TrimSpace(text) == "" {
			text = "命令未返回内容。"
		}
		m.showText(m.title, text)
		m.status = "Esc 返回列表 · r 重新读取详情"
		return m, nil
	case actionMsg:
		if m.screen != workingScreen {
			return m, nil
		}
		m.screen = detailScreen
		text := msg.out.stdout + "\n" + msg.out.stderr
		if msg.err != nil {
			text += "\n" + msg.err.Error() + "\n\n未自动重试。操作可能已被服务端接收，请查询集群状态后再决定是否重试。"
			m.showText("操作未确认成功", text)
		} else {
			m.showText("命令执行成功", text)
		}
		m.status = "Esc 返回后按 r 刷新；不会自动重复操作"
		return m, nil
	case sshMsg:
		if msg.err != nil {
			m.screen = detailScreen
			m.detailTarget = ""
			m.showText("SSH 失败", msg.err.Error())
			m.status = "Esc 返回列表 · r 刷新列表"
			return m, nil
		}
		return m, m.refresh()
	case tea.KeyPressMsg:
		key := msg.String()
		if m.screen == workingScreen {
			return m, nil
		}
		if m.screen == browseScreen && m.filtering && key != "ctrl+c" {
			return m, m.updateFilter(msg)
		}
		if m.screen == resourceScreen && key != "ctrl+c" && key != "esc" {
			return m, m.updateResourceInput(msg)
		}
		if key == "ctrl+c" || (key == "q" && m.screen != formScreen && m.screen != confirmScreen && m.screen != resourceScreen) {
			m.stopQuery()
			if m.catalogCancel != nil {
				m.catalogCancel()
			}
			return m, tea.Quit
		}
		if key == "esc" {
			if m.screen == browseScreen {
				return m, nil
			}
			m.stopQuery()
			if m.screen == resourceScreen {
				m.resourceInput.Blur()
			}
			if m.screen == confirmScreen {
				m.screen = m.returnTo
				m.pending = nil
				m.status = "已取消，未执行"
			} else if m.screen != browseScreen {
				m.screen = browseScreen
				m.status = m.listStatus()
			}
			return m, nil
		}
		if m.width < 76 || m.height < 22 {
			return m, nil
		}
		switch m.screen {
		case resourceHelpScreen:
			if key == "r" {
				cmd := m.loadCatalog()
				m.showCatalog()
				return m, cmd
			}
		case detailScreen:
			if key == "r" {
				if m.detailTarget != "" {
					return m, m.loadDetails(m.detailTarget)
				}
				return m, m.refresh()
			}
			if m.pane == jobsPane && m.detailTarget != "" && key == "e" {
				spec, err := jobPeek(m.detailTarget)
				if err != nil {
					m.status = err.Error()
					return m, nil
				}
				return m, m.runOnce(spec, "作业输出")
			}
		case formScreen:
			return m, m.updateForm(msg)
		case confirmScreen:
			if key == "n" {
				m.screen = m.returnTo
				m.pending = nil
				m.status = "已取消，未执行"
				return m, nil
			}
			if key == "y" && m.pending != nil {
				spec := *m.pending
				m.pending = nil
				return m, m.runOnce(spec, "执行中，请勿重复提交")
			}
		case browseScreen:
			if key == "/" {
				m.filtering, m.completionIndex = true, 0
				m.status = "Tab 补全 · ↑/↓ 候选 · Enter 应用 · Esc 取消"
				return m, m.activeFilterInput().Focus()
			}
			if m.pane == hostsPane || m.pane == jobsPane {
				switch key {
				case "left", "right":
					step := 12
					if key == "left" {
						step = -step
					}
					m.hostScroll = max(0, min(m.hostScroll+step, m.table.Width()-(m.width-4)))
					return m, nil
				}
			}
			switch key {
			case "tab", "1", "2":
				if key == "1" {
					m.pane = hostsPane
				} else if key == "2" {
					m.pane = jobsPane
				} else {
					m.pane = 1 - m.pane
				}
				m.table.SetRows(nil)
				m.selected = ""
				m.resize()
				return m, m.refresh()
			case "r":
				return m, m.refresh()
			}
			if m.loading {
				return m, nil
			}
			switch key {
			case "R":
				m.resourceInput.SetValue(m.resourceQueries[m.pane])
				m.resourceInput.CursorEnd()
				m.screen, m.completionIndex = resourceScreen, 0
				m.status = "LSF 原生 select 条件；Jobs 按执行主机资源匹配"
				return m, m.resourceInput.Focus()
			case "?":
				m.screen = resourceHelpScreen
				m.showCatalog()
				return m, nil
			case "h":
				if m.pane == jobsPane && m.selectedID() != "" {
					return m, m.showSelectedJobHosts()
				}
			case "x", "u", "v":
				if m.pane == jobsPane && m.selectedID() != "" {
					spec, err := jobAction(m.selectedID(), key)
					if err != nil {
						m.status = err.Error()
						return m, nil
					}
					title := map[string]string{"x": "终止作业", "u": "挂起作业", "v": "恢复作业"}[key]
					m.confirm(spec, title, browseScreen)
					return m, nil
				}
			case "e":
				if m.pane == jobsPane && m.selectedID() != "" {
					spec, err := jobPeek(m.selectedID())
					if err != nil {
						m.status = err.Error()
						return m, nil
					}
					return m, m.runOnce(spec, "作业输出")
				}
			}
			if key == "enter" {
				return m, m.openDetails()
			}
			if m.pane == hostsPane && m.selectedID() != "" {
				switch key {
				case "s":
					cmd, err := sshCommand(m.selectedID())
					if err != nil {
						m.status = err.Error()
						return m, nil
					}
					return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return sshMsg{err} })
				case "n":
					return m, m.openForm(m.selectedID())
				case "o", "c":
					action, title := "hopen", "开放主机接收作业"
					if key == "c" {
						action, title = "hclose", "关闭主机接收新作业（不是关机）"
					}
					spec, err := hostAction(m.selectedID(), action)
					if err != nil {
						m.status = err.Error()
						return m, nil
					}
					m.confirm(spec, title, browseScreen)
					return m, nil
				}
			}
		}
	}
	var cmd tea.Cmd
	switch m.screen {
	case browseScreen:
		if m.filtering {
			return m, m.updateFilter(msg)
		}
		m.table, cmd = m.table.Update(msg)
	case detailScreen, confirmScreen, resourceHelpScreen:
		m.viewport, cmd = m.viewport.Update(msg)
	case formScreen:
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	case resourceScreen:
		return m, m.updateResourceInput(msg)
	}
	return m, cmd
}

func (m *model) listStatus() string {
	if m.failed {
		return "读取失败 · 按 r 重试"
	}
	if m.pane == hostsPane {
		status := fmt.Sprintf("%d / %d 台主机 · 手动刷新", len(m.table.Rows()), len(m.hosts))
		if m.hostWarning != "" {
			status += " · " + m.hostWarning
		}
		if m.resourceQueries[m.pane] != "" {
			status += " · R: " + m.resourceQueries[m.pane]
		}
		return status
	}
	status := fmt.Sprintf("%d / %d 条作业 · 当前用户 · 手动刷新", len(m.table.Rows()), len(m.jobs))
	if m.resourceQueries[m.pane] != "" {
		status += " · R: " + m.resourceQueries[m.pane]
	}
	return status
}

var formLabels = []string{"Host", "Queue (可选)", "Job name (可选)", "Output (可选)", "Error (可选)", "Slots (可选)", "Wall (可选)", "Program", "Args (JSON)"}

func (m *model) openForm(host string) tea.Cmd {
	m.stopQuery()
	m.inputs = make([]textinput.Model, len(formLabels))
	for i := range m.inputs {
		m.inputs[i] = textinput.New()
		m.inputs[i].Prompt = ""
		m.inputs[i].SetVirtualCursor(true)
		m.inputs[i].CharLimit = 4096
	}
	m.inputs[0].SetValue(host)
	m.inputs[5].Placeholder = "4"
	m.inputs[6].Placeholder = "30 或 1:00"
	m.inputs[7].Placeholder = "/usr/bin/hostname"
	m.inputs[8].SetValue("[]")
	m.focus, m.screen = 7, formScreen
	m.status = "参数示例 [\"--count\", \"10\"]；不展开 $变量、~ 或通配符"
	m.resize()
	return m.inputs[m.focus].Focus()
}

func (m *model) updateForm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return m.reviewForm()
	case "enter":
		if m.focus == len(m.inputs)-1 {
			return m.reviewForm()
		}
		fallthrough
	case "tab", "down":
		m.inputs[m.focus].Blur()
		m.focus = (m.focus + 1) % len(m.inputs)
		return m.inputs[m.focus].Focus()
	case "shift+tab", "up":
		m.inputs[m.focus].Blur()
		m.focus = (m.focus + len(m.inputs) - 1) % len(m.inputs)
		return m.inputs[m.focus].Focus()
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return cmd
}

func (m *model) reviewForm() tea.Cmd {
	s := submission{
		host: m.inputs[0].Value(), queue: m.inputs[1].Value(), name: m.inputs[2].Value(),
		output: m.inputs[3].Value(), errorFile: m.inputs[4].Value(), slots: strings.TrimSpace(m.inputs[5].Value()),
		wall: strings.TrimSpace(m.inputs[6].Value()), program: m.inputs[7].Value(), arguments: m.inputs[8].Value(),
	}
	spec, err := s.command()
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.confirm(spec, "确认提交 job", formScreen)
	return nil
}

func (m *model) View() tea.View {
	if m.width < 76 || m.height < 22 {
		v := tea.NewView("请将终端调整为至少 76×22。\nq / Ctrl+C 退出；执行命令期间请等待。")
		v.AltScreen = true
		return v
	}
	tabs := "[1 Hosts]   2 Jobs（当前用户，含近期完成）"
	if m.pane == jobsPane {
		tabs = " 1 Hosts   [2 Jobs（当前用户，含近期完成）]"
	}
	var body, help string
	switch m.screen {
	case browseScreen:
		body = m.table.View()
		lines := strings.Split(body, "\n")
		for i := range lines {
			lines[i] = ansi.Cut(lines[i], m.hostScroll, m.hostScroll+m.width-4)
		}
		body = strings.Join(lines, "\n")
		if len(m.table.Rows()) == 0 {
			switch {
			case m.loading:
				body = "读取中…"
			case m.failed:
				body = "读取失败 · 按 r 重试"
			default:
				body = "没有记录"
				if m.pane == hostsPane && m.filterQuery != "" {
					body = "没有匹配的主机 · 按 / 修改或清空过滤条件"
				}
				if m.pane == jobsPane && m.jobQuery != "" {
					body = "没有匹配的作业 · 按 / 修改或清空过滤条件"
				}
			}
		}
		help = "↑/↓ 选择 · Enter 详情 · Tab 切换 · r 刷新 · q 退出"
		body = m.activeFilterInput().View() + "\n" + body
		if m.pane == hostsPane {
			help = "/ 过滤 · R 资源 · ? 说明 · ←/→ 滚动 · Enter 详情\nj/k 上下 · s SSH · n 提交 · o/c 开放/关闭 · Tab 切换 · r 刷新 · q 退出"
		} else {
			help = "/ 过滤 · R 资源 · h 执行主机 · x/u/v 终止/挂起/恢复 · e 输出\nj/k 上下 · Tab 切换 · r 刷新 · q 退出"
		}
		if m.filtering {
			body = m.filterEditorView()
			help = "Tab 补全 · ↑/↓ 选择候选 · Enter 应用 · Esc 取消\n清空后 Enter 显示全部；R 可按 LSF 原生资源筛选"
		}
	case resourceScreen:
		body = m.resourceEditorView()
		help = "Tab 补全 · ↑/↓ 选择候选 · Enter 查询 · Esc 取消\n清空后 Enter 取消资源筛选"
	case formScreen:
		var lines []string
		lines = append(lines, accent.Render("提交 job · 指定单个 host"))
		for i, input := range m.inputs {
			label := lipgloss.NewStyle().Width(15).Render(formLabels[i])
			if i == m.focus {
				label = accent.Render(label)
			}
			lines = append(lines, label+" "+input.View())
		}
		body = strings.Join(lines, "\n")
		help = "Tab / ↑↓ 切换字段 · Ctrl+S 预览确认 · Esc 取消\nArgs 最后一个字段按 Enter 也可预览；预览不会提交"
	default:
		body = accent.Render(m.title) + "\n" + m.viewport.View()
		help = "↑/↓ / PgUp / PgDn 滚动 · r 刷新 · Esc 返回 · q 退出"
		if m.pane == jobsPane && m.detailTarget != "" && m.screen == detailScreen {
			help = "↑/↓ 滚动 · e 查看输出 · r 重新读取 · Esc 返回 · q 退出"
		}
		if m.screen == confirmScreen {
			help = "y 执行一次 · n / Esc 取消 · ↑/↓ 滚动预览"
		} else if m.screen == workingScreen {
			help = "等待执行结束；失败或超时不会自动重试"
		}
	}
	body = lipgloss.NewStyle().Height(max(3, m.height-10)).Render(body)
	status := strings.ReplaceAll(safeText(m.status), "\n", " ")
	status = ansi.Truncate(status, m.width-4, "…")
	content := accent.Render("BUBBLE LSF") + "\n" + tabs + "\n\n" + body + "\n" + status + "\n" + help
	v := tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(content))
	v.AltScreen = true
	return v
}
