package main

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type catalogMsg struct {
	request   int
	resources []resourceDescriptor
	err       error
}

func (m *model) loadCatalog() tea.Cmd {
	if m.catalogCancel != nil {
		m.catalogCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.catalogCancel = cancel
	m.catalogRequest++
	m.catalogLoading = true
	request := m.catalogRequest
	return func() tea.Msg {
		resources, err := listResourceCatalog(ctx)
		return catalogMsg{request, resources, err}
	}
}

func (m *model) activeFilterInput() *textinput.Model {
	if m.pane == jobsPane {
		return &m.jobInput
	}
	return &m.filterInput
}

func (m *model) rebuildJobs(selected string) {
	var rows []table.Row
	for _, j := range m.jobs {
		if !matchesJob(j, m.jobFilters) {
			continue
		}
		row := table.Row{j.id, j.user, j.status, j.queue, j.host, j.name}
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

func (m *model) completionData(native bool) (map[string][]string, map[string]string) {
	values := make(map[string][]string)
	add := func(field, value string) { values[field] = append(values[field], value) }
	var localHostFields []string
	if !native && m.pane == hostsPane {
		localHostFields = append(hostFields(m.filterHosts()), "host", "name", "status", "cpu", "ncpus", "memory", "maxmem", "osrel", "max", "jobs", "run", "suspended", "reserved")
	}
	for _, h := range m.hosts {
		if native {
			for field, value := range h.resources {
				add(field, value)
			}
			add("hname", h.name)
			add("status", h.status)
			add("ncpus", h.cpu)
			add("osrel", h.osrel)
		} else if m.pane == hostsPane {
			for _, field := range localHostFields {
				value, _ := hostField(h, field)
				add(field, value)
			}
			for _, group := range h.groups {
				add("group", group)
				add("groups", group)
				add("hostgroup", group)
			}
			for _, queue := range h.queues {
				add("queue", queue)
				add("queues", queue)
			}
		}
	}
	if native {
		values["type"] = append(values["type"], "any", "local")
		values["model"] = append(values["model"], "any", "local")
		return values, nil
	}
	if m.pane == jobsPane {
		values = make(map[string][]string)
		for _, j := range m.jobs {
			for _, field := range []string{"id", "jobid", "user", "status", "stat", "queue", "name", "job_name"} {
				value, _ := jobField(j, field)
				add(field, value)
			}
			for _, host := range executionHosts(j.host) {
				add("host", host)
				add("exec_host", host)
			}
		}
		values["status"] = append(values["status"], "PEND", "RUN", "DONE", "EXIT", "PSUSP", "USUSP", "SSUSP", "UNKWN", "ZOMBI")
		return values, map[string]string{"id=": "作业 ID", "user:": "用户", "status=": "作业状态", "queue:": "队列名", "host=": "任一执行主机（支持并行作业）", "name:": "作业名称"}
	}
	fields := map[string]string{"host:": "主机名称", "status=": "Batch 状态", "cpu>=": "CPU 数量", "memory>=": "总内存；默认 G", "osrel:": "OS 版本", "queue:": "所属队列", "group:": "所属主机组", "max>=": "最大作业槽位", "jobs>=": "作业数量", "run>=": "运行作业数量"}
	for _, key := range hostFields(m.filterHosts()) {
		op := ":"
		if hostNumericField(key) {
			op = ">="
		}
		description := "主机属性 / 资源"
		for _, r := range m.resources {
			if strings.EqualFold(r.name, key) {
				switch strings.ToLower(r.kind) {
				case "numeric":
					op = ">="
				case "string":
					op = ":"
				case "boolean":
					op = "="
				}
				description = r.kind + " · " + r.description
			}
		}
		fields[key+op] = description
	}
	return values, fields
}

func (m *model) completions(input textinput.Model, native bool) []completion {
	values, fields := m.completionData(native)
	return completeInput(input, native, m.resources, values, fields)
}

func (m *model) completeKey(key string, input *textinput.Model, native bool) {
	items := m.completions(*input, native)
	if len(items) == 0 {
		m.completionIndex = 0
		return
	}
	m.completionIndex = min(m.completionIndex, len(items)-1)
	switch key {
	case "up":
		m.completionIndex = (m.completionIndex + len(items) - 1) % len(items)
	case "down":
		m.completionIndex = (m.completionIndex + 1) % len(items)
	case "tab":
		applyCompletion(input, items[m.completionIndex])
		m.completionIndex = 0
	}
}

func (m *model) suggestionView(input textinput.Model, native bool) string {
	items := m.completions(input, native)
	if len(items) == 0 {
		return "无补全候选；可继续输入条件。"
	}
	selected := min(m.completionIndex, len(items)-1)
	start := max(0, selected-2)
	end := min(len(items), start+4)
	var lines []string
	for i := start; i < end; i++ {
		marker := "  "
		if i == selected {
			marker = "> "
		}
		line := marker + items[i].value + " · " + items[i].description
		line = ansi.Truncate(safeText(line), m.width-4, "…")
		if i == selected {
			line = accent.Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, fmt.Sprintf("候选 %d/%d · Tab 插入 · ↑/↓ 选择", selected+1, len(items)))
	return strings.Join(lines, "\n")
}

func (m *model) filterEditorView() string {
	input := m.activeFilterInput()
	text := input.View() + "\n" + m.suggestionView(*input, false) + "\n\n" + m.table.View()
	lines := strings.Split(text, "\n")
	if len(lines) > m.height-9 {
		lines = lines[:m.height-9]
	}
	for i := range lines {
		lines[i] = ansi.Cut(lines[i], 0, m.width-4)
	}
	return strings.Join(lines, "\n")
}

func (m *model) resourceEditorView() string {
	target := "Hosts"
	if m.pane == jobsPane {
		target = "Jobs 的执行主机（任一主机符合即保留）"
	}
	lines := []string{
		accent.Render("按资源选择 · " + target),
		m.resourceInput.View(),
		"",
		m.suggestionView(m.resourceInput, true),
		"",
		"数值用 >= / <=，字符串用 ==，布尔资源直接写名称。",
		"支持 &&、||、! 和括号；条件交给 LSF 解析。",
		"ncpus 为 CPU/槽位；cpu 是 r1m 别名；内存遵循 LSF 单位。",
	}
	if m.catalogLoading {
		lines = append(lines, "正在读取集群资源描述符…")
	} else if m.catalogError != "" {
		lines = append(lines, "lsinfo 读取失败，使用内置说明；按 ? 查看诊断。")
	}
	text := strings.Split(strings.Join(lines, "\n"), "\n")
	for i := range text {
		text[i] = ansi.Truncate(text[i], m.width-4, "…")
	}
	return strings.Join(text[:min(len(text), m.height-9)], "\n")
}

func (m *model) updateResourceInput(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "tab", "up", "down":
			m.completeKey(key.String(), &m.resourceInput, true)
			return nil
		case "enter":
			requirement, err := selectionRequirement(m.resourceInput.Value())
			if err != nil {
				m.status = err.Error()
				return nil
			}
			m.resourceQueries[m.pane] = requirement
			m.resourceInput.Blur()
			return m.refresh()
		}
	}
	var cmd tea.Cmd
	m.resourceInput, cmd = m.resourceInput.Update(msg)
	m.completionIndex = 0
	return cmd
}

func (m *model) showCatalog() {
	var lines []string
	lines = append(lines, "资源描述符来自 lsinfo -r -l；Tab 按类型补全。", "Numeric：数值比较；String：字符串比较；Boolean：名称或 !名称。", "", "按 R 输入 select[type==any && ncpus>=16 && maxmem>=65536]。", "Jobs 按当前执行主机匹配；未分配主机的 PEND 作业不匹配。", "内存条件遵循 LSF 单位和集群配置；本地 / memory 默认 G。", "cpu 是 r1m（负载）别名，ncpus 才是 CPU 数量/槽位。", "数字条件默认 type==local；跨类型查询时显式加 type==any。", "")
	if m.catalogLoading {
		lines = append(lines, "正在读取集群资源描述符…", "")
	}
	if m.catalogError != "" {
		lines = append(lines, "lsinfo 读取失败，以下为内置说明或上次成功结果：", m.catalogError, "")
	}
	for _, r := range m.resources {
		lines = append(lines, fmt.Sprintf("%s [%s] · %s", r.name, r.kind, r.description))
		if r.builtin != "" || r.dynamic != "" || r.consumable != "" {
			lines = append(lines, fmt.Sprintf("  BUILTIN=%s DYNAMIC=%s CONSUMABLE=%s RELEASE=%s ORDER=%s INTERVAL=%s", r.builtin, r.dynamic, r.consumable, r.release, r.order, r.interval))
		}
	}
	m.showText("LSF 资源描述符 · r 重新读取", strings.Join(lines, "\n"))
	m.status = "↑/↓ 滚动 · r 重读 lsinfo · Esc 返回"
}

func (m *model) showSelectedJobHosts() tea.Cmd {
	id := m.selectedID()
	for _, j := range m.jobs {
		if j.id != id {
			continue
		}
		names := executionHosts(j.host)
		if len(names) == 0 {
			m.status = "此作业尚未分配执行主机"
			return nil
		}
		var conditions []string
		for _, name := range names {
			conditions = append(conditions, "hname=='"+name+"'")
		}
		m.pane = hostsPane
		m.resourceQueries[hostsPane] = "select[" + strings.Join(conditions, " || ") + "]"
		m.filterQuery, m.filters = "", nil
		m.filterInput.SetValue("")
		m.table.SetRows(nil)
		m.selected = ""
		m.resize()
		return m.refresh()
	}
	return nil
}

func (m *model) filterHosts() []host {
	// Catalog names remain filterable even when a host has no reported value.
	catalog := host{resources: make(map[string]string), resourceKinds: make(map[string]string)}
	for _, r := range m.resources {
		catalog.resources[r.name] = ""
		catalog.resourceKinds[r.name] = r.kind
	}
	return append(append([]host(nil), m.hosts...), catalog)
}
