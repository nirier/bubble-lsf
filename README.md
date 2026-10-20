# Bubble LSF

使用 Go 和 Bubble Tea 构建的 IBM LSF 终端界面。在主机列表中查看执行节点、打开机器详情、通过 SSH 登录，以及向选定主机提交作业。

## 安装与启动

需要已配置好的 LSF 客户端环境。启动前加载集群提供的环境脚本，确保 `bhosts` 和 `bjobs` 位于 `PATH` 中。

从 [Releases](https://github.com/nirier/bubble-lsf/releases) 下载对应平台的压缩包，或用 Go 1.27.1 及以上从源码构建：

```sh
go build -o bubble-lsf .
./bubble-lsf
```

`./bubble-lsf -version` 打印构建版本。

也可以使用 `go run .`。`./bubble-lsf -h` 查看帮助，无须连接集群。终端至少为 **76 列 × 22 行**。

| 命令 | 用途 |
| --- | --- |
| `bhosts`、`bjobs` | 启动必需；主机列表和作业查询 |
| `lshosts` | 主机静态规格、自定义标签及静态共享资源 |
| `bqueues`、`bmgroup` | 主机所属队列和主机组；展开队列配置中的主机组 |
| `lsinfo` | 读取集群资源描述符，补全资源名称并展示类型、说明 |
| `lsload` | 主机动态负载及 External / ELIM 动态共享资源 |
| `bsub` | 提交作业 |
| `bkill`、`bstop`、`bresume` | 终止、挂起、恢复所选作业；执行前确认，需要相应权限 |
| `bpeek` | 查看作业标准输出；通常仅对仍在运行的作业有效 |
| `badmin` | 开放或关闭主机接收新作业，需要相应权限 |
| `ssh` | 交互登录主机，沿用本机 SSH 配置和认证 |

可选命令缺失或权限不足时，对应操作会显示错误；主机详情中已经取得的其他结果仍会保留。

## 操作

| 页面 | 按键 | 行为 |
| --- | --- | --- |
| 列表 | `Tab` / `1` / `2` | 切换 Hosts 和 Jobs |
| 列表 | `j` / `k`、`↑` / `↓`、`PgUp` / `PgDn` | 选择记录、翻页 |
| 列表 | `Enter` | 查看所选主机或作业详情 |
| 列表 | `r` | 刷新列表，保留仍存在的选中项 |
| Hosts | `s` | SSH 登录；退出 SSH 后恢复界面并刷新 |
| Hosts | `n` | 为选中主机填写提交表单 |
| Hosts | `o` / `c` | 预览开放 / 关闭主机接收作业的命令 |
| 列表 | `/` | 编辑本地字段过滤；`Enter` 应用，`Esc` 取消编辑 |
| 列表 | `R`（大写） | 按 LSF 原生资源条件选择 Hosts / Jobs 的执行主机 |
| 列表 | `?` | 查看资源类型、说明和元数据；此页 `r` 重读资源目录 |
| 过滤 / 资源编辑 | `Tab`、`↑` / `↓` | 插入补全；选择候选；`Enter` 应用，`Esc` 取消 |
| Jobs | `h` | 切换 Hosts，选择所选作业的执行主机（含并行作业） |
| Jobs | `x` / `u` / `v` | 预览后确认执行 `bkill` / `bstop` / `bresume`；取消不执行 |
| Jobs / 作业详情 | `e` | 读取 `bpeek` 输出，只读，不需要确认 |
| Hosts | `←` / `→` | 横向滚动，查看其他列 |
| 详情 | `j` / `k`、`↑` / `↓`、`PgUp` / `PgDn` | 滚动详情 |
| 详情 | `r` | 重新查询；查询失败页和操作结果页则刷新列表 |
| 表单 | `Tab` / `Shift+Tab`、`↑` / `↓` | 切换字段 |
| 表单 | `Ctrl+S` | 校验并预览；最后一个字段也可按 `Enter` |
| 确认 | `y` | 执行一次 |
| 确认 | `n` / `Esc` | 取消执行；提交预览返回原表单 |
| 详情 / 表单 | `Esc` | 返回列表 |
| 全局 | `Ctrl+C` | 退出；命令执行期间需等待结果 |
| 列表 / 详情 | `q` | 退出 |

Jobs 使用 `bjobs -a` 查询当前用户的作业，包含 LSF 仍保留的近期完成记录。列表由用户手动刷新。

主机的 `S/U` 列分别显示系统挂起 / 用户挂起的作业数。`c` 调用 `badmin hclose`，表示关闭主机接收新作业，**不是关闭机器电源**；`o` 调用 `badmin hopen`。两者执行前均显示目标和命令供确认。

## 主机信息与过滤

Hosts 保留 `CPU`（`ncpus`）、`MEMORY`（`maxmem`）、`OSREL`、`QUEUES`、状态和作业列，随后显示全部已采集的属性和自定义资源列。按 `←` / `→` 横向滚动，`Enter` 查看完整字段快照及原始命令详情。`j` 向下、`k` 向上移动记录。

| 属性 | 数据来源与字段 |
| --- | --- |
| 静态硬件和系统 | `lshosts -w`、`lshosts -l`：`ncpus`、`nprocs`、`ncores`、`nthreads`、`cpuf`、`maxmem`、`maxswp`、`maxtmp`、`model`、`type`、`ostype` 等 |
| 动态负载 | `lsload -l`：`r15s`、`r1m`、`r15m`、`ut`、`mem`、`swp`、`pg`、`io`、`it`、`tmp` 及额外负载列 |
| 自定义静态资源 | `lshosts` 的 `RESOURCES`，以及 `lshosts -s`：布尔标签、字符串和数值资源 |
| External / ELIM 资源 | `lsload -l` 的扩展列、`lsload -s` 的共享实例；例如 `OSREL`、`GPU_TEMP`、`GPU_UTIL`、`license_xxx`。静态字符串实例也可能由 `lshosts -s` 提供 |
| 调度属性 | `bhosts -w`、`bhosts -l`：`MAX`、`JL/U`、`JL/P`、`NJOBS`、`RUN`、`SSUSP`、`USUSP`、`RSV`、`loadSched` / `loadStop` 等 |
| 组和队列 | `bmgroup -w -r` 展开的主机组、`bqueues -l` 中的 `HOSTS` 配置 |

列根据实际返回的字段自动扩展，不固定自定义资源名称。缺失字段显示 `-`。CPU 数量与 `MAX` 槽位上限分开；**`memory` / `maxmem` 表示总内存，`mem` 表示实时可用内存**。队列列表示配置范围，不代表当前用户的提交权限。共享资源按 `LOCATION` 绑定到对应主机，不会把其他实例的值分配给该主机。

按 `/` 输入本地过滤，`Enter` 应用。全部采集字段均支持过滤；编辑时按 `Tab` 补全字段和已知值。多个条件以空格分隔，按 AND 匹配，例如：

```text
cpu>=16 memory>=64G mem>=32G ut<80%
ssd=1 rack_id='rack 01' group=workers queue=normal
OSREL:KLV10 GPU_TEMP<70 license_xxx>0
jl/u>=8 ssusp=0 loadsched.mem>=4G loadstop.r1m<8
load.status=busy batch.status=ok
```

数值支持 `=`、`!=`、`>`、`>=`、`<`、`<=`；文本支持 `:` 包含、`=` 等于、`!=` 不等于。资源名可含数字、下划线、点、连字符，`JL/U` / `JL/P` 可直接输入。内存支持 K/M/G/T、GB/GiB 等写法，省略单位默认 G；利用率可写 `80%` 或 `0.8`。带空格的值用引号包裹。本地字段名和值不区分大小写。纯文本在全部字段名和值中搜索。

同名字段可用来源前缀区分：`static.*`、`load.*`、`batch.*`、`resource.*`、`external.*`；`status` 为 Batch 状态，`load.status` 为 LIM 状态。阈值使用 `loadsched.r1m`、`loadstop.mem` 等名称；Batch 当前负载和预留负载使用 `total.mem`、`reserved.mem`。`group` / `groups` 和 `queue` / `queues` 支持成员匹配。

OSREL 只显示集群公布的值，不推测操作系统版本。附加查询失败时保留其他来源的结果，并在状态栏显示诊断；缺失或未知资源值不会匹配比较条件。清空 `/` 后按 `Enter` 取消本地过滤；过滤在刷新和页面切换后保留，且不影响 Jobs。

## 资源描述符补全与选择

按 **`R`** 输入资源条件，例如：

```text
select[type==any && ncpus>=16 && maxmem>=65536]
```

也可直接输入括号内的条件，程序补上 `select[...]`。输入资源名称前缀，按 `Tab` 按类型补全：Numeric 插入 `>=`，String 插入 `==`，Boolean 插入资源名。候选显示资源类型及说明；字符串的已知值会加上引号，值保留大小写。资源值候选来自当前加载的主机列表，并非资源所有可能的取值。

资源目录启动时异步读取 **`lsinfo -r -l`**，包含集群自定义资源。按 **`?`** 查看 `TYPE`、`DESCRIPTION`、`BUILTIN`、`DYNAMIC`、`CONSUMABLE`、`RELEASE`、`ORDER`、`INTERVAL`。读取失败时保留上次结果或内置说明；说明页按 `r` 重试，不妨碍主机和作业查询。

条件通过单独的命令参数传给 **`bhosts -w -R`**，由 LSF 解析，支持 `&&`、`||`、`!`、括号和集群自定义资源。此入口只接受 `select` 条件，拒绝 `rusage` 等会被主机查询忽略的段。原生资源名称和字符串值区分大小写，内存单位遵循 LSF 配置。**原生 `cpu` 是 `r1m` 负载别名；CPU 数量应使用 `ncpus`**。当 `PARALLEL_SCHED_BY_SLOT=Y` 时，原生 `ncpus` 表示槽位数。只有数值条件时 `bhosts` 默认 `type==local`；需要跨类型选择时显式写 `type==any`。

在 Jobs 按 `R` 使用同样的条件，程序查询匹配主机并保留**至少一个执行主机符合条件**的作业。支持 `4*hostA:8*hostB` 这样的并行作业；没有执行主机的待调度作业不会匹配。这里比较的是执行主机当前资源，已完成作业也按其主机当前信息匹配。Hosts / Jobs 的资源条件分别保存，刷新和切换页面后保留，且与各自 `/` 本地过滤同时生效。

Jobs 列表增加 `SUBMIT` 列。`/` 支持 `id`、`user`、`status`、`queue`、`host`、`name`、`submit` 字段及字段值补全，例如：

```text
status=RUN queue:normal host=compute-01
name='parallel job'
```

`host=...` 匹配并行作业的任一执行主机。带空格的名称可用单引号或双引号包裹。清空 `/` 输入只取消本地过滤；清空 `R` 输入只取消原生资源条件。

Jobs 按 `h` 跳到执行主机列表，会替换 Hosts 资源条件并清空 Hosts 本地条件；未分配执行主机的作业显示提示。

官方文档来源、资源描述符字段含义及表达式示例见 [LSF 资源说明](docs/lsf-resources.md)。

## 提交作业

在 Hosts 选中目标主机，按 `n`，填写以下字段：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| Host | `compute-01` | 单个执行主机，对应 `bsub -m` |
| Queue | `normal` | 可选，对应 `-q` |
| Job name | `example` | 可选，对应 `-J` |
| Output | `logs/job.%J.out` | 可选，对应 `-o`，路径遵循集群的 LSF 规则 |
| Error | `logs/job.%J.err` | 可选，对应 `-e` |
| Slots | `4` | 可选，对应 `-n`，正整数 |
| Wall | `30` 或 `1:00` | 可选，对应 `-W`，分钟或 `[hour:]minute:second` |
| Program | `/usr/bin/python3` | 程序名称或执行节点上可用的路径 |
| Args | `["train.py", "--epochs", "10"]` | JSON 字符串数组，空白或 `[]` 表示无参数 |

程序和参数按字面值传入，不自动展开 `$变量`、`~`、通配符、管道或重定向。需要这些行为时，准备脚本并将其路径填写到 Program。程序、参数、名称和路径不接受换行或控制字符。

按 `Ctrl+S` 查看目标 host、LSF 选项和完整提交脚本，再按 `y` 提交。指定主机使用 LSF 调度约束，作业仍由调度器安排，可能处于等待状态。

执行失败或超时不会自动重试。此时请求可能已被集群接收，应先刷新查询集群状态，再决定是否重新提交或执行管理操作。

## 兼容性与排错

主机列表读取 `bhosts -w` 的标准九列格式：

```text
HOST_NAME STATUS JL/U MAX NJOBS RUN SSUSP USUSP RSV
```

暂不支持凝聚主机组。查询会移除 `LSB_BHOSTS_FORMAT`、`LSF_LSHOSTS_FORMAT`、`LSF_LSLOAD_FORMAT` 自定义格式，并将命令语言设为 `C`。`lshosts -w` 输出至少需要 `HOST_NAME`、`ncpus`、`maxmem` 列。

作业列表要求客户端支持以下命令及字段：

```sh
bjobs -a -noheader -o "jobid user stat queue exec_host job_name submit_time delimiter='|'"
```

如果客户端不支持 `-o` 或这些字段，界面会显示错误，不会将其当作空列表。可在目标集群运行上述只读命令检查兼容性；本项目尚未在真实集群上验证。只有明确的 `No job found` / `No unfinished job found` 或成功的空输出会按空作业列表处理。

每条 LSF 命令设置 15 秒超时。主机列表先查询 `bhosts -w`，再并行批量查询 `lshosts -w/-l/-s`、`bhosts -l`、`lsload -l/-s`、`bqueues -l` 和 `bmgroup -w -r`，不逐台发起查询。主机详情读取长格式配置、负载、共享资源、队列和主机组。返回或切换页面会取消当前查询，迟到的结果不会覆盖新页面。命令错误会保留诊断内容；外部输出在显示前会去除终端控制序列。

原生资源筛选给主机查询增加 `-R`；Jobs 资源筛选在 `bjobs` 后额外查询 `bhosts -w -R`。资源目录独立查询，有自己的取消 context 和请求标识；切换列表不会重读目录。资源表达式的 LSF 报错会显示在查询失败页，不会当作没有匹配结果。

SSH 使用系统 `ssh -- HOST`，沿用现有主机密钥检查。登录失败时，请先在终端检查相同主机的 SSH 连接。

## 开发与验证

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

测试使用样例输出和临时替身命令，无须 LSF 集群，不会提交真实作业、修改主机状态或发起 SSH 登录。覆盖输出解析、参数引用、资源描述符、光标中间补全、原生查询参数、并行作业匹配，以及界面刷新、取消确认、单次执行和终端尺寸。

源码职责：`main.go` 检查环境并启动；`lsf.go` 封装 CLI、基础解析与校验；`host_info.go` 聚合主机各来源、队列和主机组；`host_attributes.go` 解析并保留属性及共享资源实例；`host_filter.go` / `job_filter.go` 解析本地过滤；`resources.go` 读取资源目录并查询匹配主机 / 作业；`completion.go` 完成光标位置的字段 / 值补全；`tui.go` / `resource_ui.go` 管理界面状态和异步命令；`*_test.go` 验证适配层与交互流程。
