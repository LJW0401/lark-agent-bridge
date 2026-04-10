//go:build windows

package platform

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// EnsureRoot 检查是否有管理员权限，没有则通过 UAC 提权重新执行自己
func EnsureRoot() {
	if isAdmin() {
		return
	}

	// 通过 ShellExecuteEx 以 "runas" 触发 UAC 提权
	exe, _ := os.Executable()
	args := strings.Join(os.Args[1:], " ")

	verb, _ := syscall.UTF16PtrFromString("runas")
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	argsPtr, _ := syscall.UTF16PtrFromString(args)
	cwdPtr, _ := syscall.UTF16PtrFromString("")

	err := windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_SHOWNORMAL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "需要管理员权限，请右键以管理员身份运行")
		os.Exit(1)
	}

	// 当前进程退出，让提权后的新进程接管
	os.Exit(0)
}

func isAdmin() bool {
	_, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	return err == nil
}
