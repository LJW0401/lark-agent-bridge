//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// EnsureRoot 检查是否为 root，不是则用 sudo 重新执行自己
func EnsureRoot() {
	if os.Geteuid() == 0 {
		return
	}

	// 用 sudo 重新执行自己，保留所有参数
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		fmt.Fprintln(os.Stderr, "需要 root 权限，请使用 sudo 运行")
		os.Exit(1)
	}

	// execve 替换当前进程为 sudo + 自己
	args := append([]string{sudoPath}, os.Args...)
	if err := syscall.Exec(sudoPath, args, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "sudo 执行失败: %v\n", err)
		os.Exit(1)
	}
}
