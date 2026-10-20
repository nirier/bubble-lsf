package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// version is injected by the release workflow: -ldflags "-X main.version=v1.0.0".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Bubble LSF — 主机、job 和 SSH 的终端界面\n\n用法: bubble-lsf\n需要已配置的 LSF 客户端（bhosts、bjobs）。\n可选命令: bsub、bkill、bstop、bresume、bpeek、badmin、lshosts、bqueues、bmgroup、lsinfo、lsload、ssh。\n\n快捷键: Tab 切换，Enter 详情，r 刷新，q 退出。\n列表: / 字段过滤，R 资源筛选，? 资源说明；编辑时 Tab 补全。\n主机页: ←/→ 滚动，j/k 上下移动，s SSH，n 提交，o 开放接收，c 关闭接收。\n作业页: h 查看执行主机，s 进入源目录，x/u/v 终止/挂起/恢复（确认后执行），e 查看输出；R 按执行主机的资源筛选。")
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	var missing []string
	for _, name := range []string{"bhosts", "bjobs"} {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "缺少 LSF 命令: %s\n请先加载集群的 LSF 环境，使客户端命令位于 PATH 中。\n", strings.Join(missing, ", "))
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := tea.NewProgram(newModel(ctx)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TUI:", err)
		os.Exit(1)
	}
}
