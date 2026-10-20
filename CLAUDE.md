# CLAUDE.md

## 项目状态与目标

本项目使用 Go 和 Bubble Tea v2 开发 IBM LSF 终端交互界面。当前已实现：

- `bhosts` 主机列表，补充全量静态属性、动态负载、自定义 / External / ELIM 资源、调度阈值、主机组和队列；`/` 组合过滤和左右键横向滚动；`badmin hopen` / `hclose` 的预览、确认与执行。
- 当前用户的 job 列表（含提交时间）和 `bjobs -l` 详情；`x`/`u`/`v` 确认后执行 `bkill`/`bstop`/`bresume`，`e` 读取 `bpeek`。
- `lsinfo -r -l` 资源描述符目录和说明，按类型补全自定义资源；`R` 原生 select 筛选 Hosts 及执行主机匹配的 Jobs；Jobs 本地字段过滤及 Jobs 跳转执行主机；j/k 仅上下移动。
- 指定单个目标 host 的提交表单，支持 queue、job name、output、error、slots、wall、program 和 JSON 参数数组，确认后调用 `bsub -m`。
- `bhosts -l`、`lshosts -l`、`lsload -l`、共享资源与拓扑信息机器详情，以及交还终端的交互 SSH 登录。Jobs 的 `s` 登录执行主机并进入 `bjobs -l` 的绝对 `CWD`。
- 查询取消、过期结果隔离、失败重试、刷新保留选中项、最小终端尺寸检查。

使用方式、快捷键及 LSF 输出兼容性见 `README.md`。尚未验证真实集群；不得把本地替身测试视为集群兼容性验证。

## 开发命令

Go 模块为 `bubble-lsf`，Go 版本要求见 `go.mod`（当前 1.27.1）。入口位于仓库根目录，不引入额外构建工具或 lint 框架。推送 `v*` tag 时 `.github/workflows/release.yml` 交叉编译 linux/darwin/windows 的 amd64 与 arm64，并附带 SHA256SUMS 发布到 GitHub Release。版本由 `-ldflags -X main.version` 注入，本地构建为 `dev`。

```sh
go run .
go build -o bubble-lsf .
go test ./...
go test -race ./...
go test ./... -run '^TestName$' -count=1
go vet ./...
gofmt -w .
```

真实操作依赖已配置的 LSF 客户端、当前用户的集群权限及 `PATH` 中的命令；SSH 依赖系统 `ssh` 和现有认证配置。不得假定开发环境能连接 LSF 集群。

## 架构

- `main.go`：命令行帮助、检查必需的 `bhosts` / `bjobs`、创建根 context 并运行 Bubble Tea program。
- `tui.go`：Model / Update / View 管理列表、详情、提交表单、确认和执行中状态；表格、输入框、滚动视口使用 Bubbles v2。外部调用通过 `tea.Cmd` 返回消息，不能阻塞 `Update` / `View`。查询带递增请求标识及可取消 context，忽略过期结果。
- `lsf.go`：集中定义命令参数、15 秒单命令超时、环境规范化、输出解析、主机 / job ID 校验和提交脚本引用。适配层向 TUI 返回结构化数据，界面不解析 CLI 输出。
- `host_info.go`：在 `bhosts -w` 后并行批量读取 `lshosts -w/-l/-s`、`bhosts -l`、`lsload -l/-s`、`bqueues -l`、`bmgroup -w -r`，聚合完整主机属性、队列和主机组。附加查询失败保留其他来源并返回诊断。
- `host_attributes.go`：保留长格式 / 宽表的全部字段和来源前缀，解析 RESOURCES 及共享实例 LOCATION；同名字段可用 static/load/batch/resource/external 前缀过滤。表格随实际资源字段扩展。
- `host_filter.go` / `job_filter.go`：解析各自本地过滤，支持引号包裹文本，全部主机属性及自定义资源比较和多条件 AND；memory/maxmem 为总内存，mem 为动态可用内存；各页面分别保存条件。
- `resources.go`：读取并校验集群资源描述符元数据；select 条件交由 `bhosts -w -R` 解析，Jobs 与匹配主机按执行主机名关联，支持并行作业的槽位前缀。这里只支持 select，避免 rusage 等被 bhosts 忽略。原生 cpu 是 r1m 别名，不得与本地 CPU 数量混淆。
- `completion.go` / `resource_ui.go`：光标位置补全、类型和描述候选、资源编辑及说明页、Jobs 过滤及作业跳转执行主机。资源目录单独异步查询，有独立取消 context 和请求标识；失败保留上次目录或内置说明。
- `lsf_test.go`：样例解析及本地替身命令测试。`fakeCommands` 将 PATH 设置为临时目录，避免落到真实 LSF 安装。
- `tui_test.go`：消息和按键驱动的交互回归测试，包括取消、重试、过期响应、重复确认以及中文界面尺寸。

保持职责清晰，按实际复杂度拆分，不预先建立多层接口或插件框架。

## 外部命令与交互边界

- 查询和管理使用 `os/exec` 独立参数，不将主机名或表单输入拼接成 `sh -c`。主机只允许单个名称，拒绝 `all`、选项及 `user@host`。
- 主机管理是开放 / 关闭接收作业，不是机器开关机；执行前确认目标和操作。提交同样先展示目标 host、选项及脚本供用户确认。
- `bsub` 从 stdin 接收脚本，程序及每个参数独立进行 shell 引用，拒绝控制字符；表单的程序 / 参数与 LSF 选项分离。不要用 SSH 执行命令冒充 LSF 作业。
- 主机列表支持 `bhosts -w` 标准九列格式；`lshosts -w` 至少需要 `HOST_NAME`、`ncpus`、`maxmem` 列。队列信息来自配置中的 HOSTS，并非当前用户权限。job 列表要求 `bjobs -o` 支持指定字段和分隔符。不假定所有 LSF 版本都有相同输出或 JSON；无法识别时明确报错。
- 保留有用的命令诊断。认证失败或查询失败不能显示成空列表。提交和管理操作不得因刷新、重复按键或错误重试而自动重复执行。
- SSH 使用 `tea.ExecProcess` 交还终端，退出后恢复 TUI；沿用用户配置及主机密钥检查，不内置密码处理或关闭校验。
- 外部命令输出显示前经 `safeText` 清理终端控制序列。
- 普通测试必须离线运行，不得提交真实 job、改变主机状态或连接真实 SSH 主机。
- 官方 LSF 10.1 文档来源、资源类型、单位和选择语义见 `docs/lsf-resources.md`。原生表达式保留大小写及集群单位；Jobs 资源筛选是按执行主机当前资源匹配，没有执行主机的作业不匹配。

变更实现后同步维护 README 和本文件中的命令、状态与架构说明。
