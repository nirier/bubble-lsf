# LSF 资源描述符与选择表达式

本文依据 IBM Spectrum LSF 10.1 官方文档整理，作为资源补全和选择功能的实现依据。集群自己的资源定义、客户端版本和配置以实际 `lsinfo` 输出为准。

## 官方文档

- [lsinfo 命令与资源元数据](https://www.ibm.com/docs/api/v1/content/SSWRJV_10.1.0/lsf_command_ref/lsinfo.1.html?lang=en)
- [bhosts 命令与 -R](https://www.ibm.com/docs/api/v1/content/SSWRJV_10.1.0/lsf_command_ref/bhosts.1.html?lang=en)
- [Selection string：资源类型、运算符、别名和默认主机类型](https://www.ibm.com/docs/api/v1/content/SSWRJV_10.1.0/lsf_admin/select_string.html?lang=en)
- [Resource requirement strings：段落结构和资源单位](https://www.ibm.com/docs/api/v1/content/SSWRJV_10.1.0/lsf_admin/res_req_strings_about.html?lang=en)

查询时读取的官方正文标识分别为 `SSWRJV_10.1.0/lsf_command_ref/lsinfo.1.html`、`lsf_command_ref/bhosts.1.html`、`lsf_admin/select_string.html`、`lsf_admin/res_req_strings_about.html`。应用运行时读取本机 LSF 命令，不依赖访问文档网站。

## 描述符和资源值

`lsinfo -r -l` 返回资源名称、描述和元数据，既包含内置资源，也包含管理员配置的资源。资源描述符定义如何使用某个资源；它与资源在某台主机上的实际取值不同。应用按资源类型生成补全，已知值取自当前列表中的主机配置。

| 字段 | 含义 |
| --- | --- |
| `RESOURCE_NAME` | 在表达式里引用的资源名称 |
| `DESCRIPTION` | 资源用途说明，由 LSF 或管理员提供 |
| `TYPE` | `Numeric`、`String`、`Boolean` |
| `ORDER` | 数值资源的排序方向：`Inc` 表示负载增加时值增加，`Dec` 表示值减少；非数值为 `N/A` |
| `INTERVAL` | 资源值更新间隔，单位秒；0 表示不变化 |
| `BUILTIN` | 是否为 LIM 内置资源；`No` 表示站点自定义资源 |
| `DYNAMIC` | 是否为随时间变化的负载指标 |
| `CONSUMABLE` | 是否配置为可消耗的数值资源 |
| `RELEASE` | 数值共享资源在作业挂起时是否释放；不适用时为 `N/A` |

内置示例：`ncpus`、`maxmem`、`mem`、`ut`、`r1m`、`type`、`model`、`hname`、`server`。`osrel`、GPU 标签或许可证名称是否存在、是什么类型，需查本集群描述符，程序不会将它们当作所有集群都必备的内置资源。

资源目录查询失败时，应用保留上次成功结果或内置说明，并在资源说明页显示诊断。可继续手动输入自定义资源表达式，由 LSF 校验。

## 选择表达式

`select[...]` 针对每台主机求值，非零表示符合条件。应用通过 `bhosts -w -R EXPRESSION` 查询结果，不在本地重新实现 LSF 表达式解释器。

| 类型 | 示例 | 补全行为 |
| --- | --- | --- |
| Numeric | `ncpus>=16`、`ut<0.5` | 资源名后补 `>=`，可继续修改运算符 |
| String | `type=='X86_64'`、`osrel=='7.9'` | 资源名后补 `==`，已知字符串值加引号 |
| Boolean | `gpu_ready`、`!gpu_ready` | 插入资源名称 |

`osrel`、`gpu_ready` 示例仅适用于集群确实定义了对应资源的情况。字符串共享资源值应加单引号。资源表达式保留大小写，严格语法下资源名称和字符串比较区分大小写。

选择条件支持数值与比较运算、`&&`、`||`、`!` 和括号。`defined(resource)` 用于检测是否定义了特定站点共享资源，但文档对可作为参数的资源有约束，不能对所有内置资源或动态数值指标任意使用；最终由 LSF 校验。

```text
select[type==any && ncpus>=16 && maxmem>=65536]
select[type=='X86_64' && (gpu_ready || maxmem>=131072)]
select[osrel=='7.9' && ut<0.5]
```

几个需要区分的含义：

- 原生 `cpu` 是 `r1m` 的别名，表示 1 分钟 CPU 运行队列负载。它不表示 CPU 数量。本地 `/ cpu>=16` 才是按列表 CPU 数量过滤。
- 原生 `ncpus` 通常表示 CPU 数量；配置 `PARALLEL_SCHED_BY_SLOT=Y` 时，它在 select 中表示槽位数，而 `lshosts` 的 ncpus 列仍依照 `EGO_DEFINE_NCPUS`。
- `maxmem` 是主机总内存；`mem` 是可用内存，两者不能互换。`ut` 在选择条件中使用 0.0–1.0，而显示的利用率可能是百分比。
- `bhosts` 没有显式指定 type 时通常使用 `type==local`；出现字符串或布尔资源时默认改为 `type==any`。跨类型查询应显式写 `type==any`，应用不会悄悄改变输入的主机类型条件。
- 原生资源条件的数值单位遵循 LSF 资源定义和配置。文档说明 `mem`、`swp` 在 select / rusage 中默认使用 MB，`LSF_UNIT_FOR_LIMITS` 可调整相关单位；支持哪些显式单位还取决于命令和版本。本地 `/ memory>=64G` 使用程序自己的二进制内存换算，不能直接与原生表达式混用。

完整的 LSF 资源需求字符串还有 `order`、`rusage`、`span`、`same`、`affinity` 等段，分别承担排序、预留资源、分布约束等用途。官方 bhosts 文档明确说明不能用 `rusage` 选择主机，因为它会被忽略。因此本应用的 `R` 查询入口只接受 select 条件，拒绝其他段，防止把资源预留误当作过滤条件。

## 从主机资源选择作业

Jobs 的 `R` 查询先读取当前用户作业，再用 `bhosts -R` 获取符合资源条件的主机。作业有任一执行主机在匹配集合中就保留，使用 LSF 命令返回的完整主机名关联。

例如，`4*hostA:8*hostB` 表示在两台主机上分配槽位的并行作业；当 hostB 满足条件时，该作业被保留。未分配执行主机的待调度作业不参与这种匹配。已完成作业使用仍可查询到的执行主机当前资源，不重建历史资源快照。

这项功能比较执行主机资源，不解析作业提交时的资源需求或预留量。是否真正能运行新作业还取决于调度策略、队列、用户权限、资源预留等条件。

查询属于只读操作，资源补全和条件编辑不会提交作业或修改主机状态。语法错误、未知资源、认证和权限错误保留 LSF 诊断；成功且无匹配时显示空结果。
