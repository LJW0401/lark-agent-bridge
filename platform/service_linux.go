//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitFilePath = "/etc/systemd/system/" + ServiceName + ".service"

type linuxService struct{}

// NewService 创建 Linux 平台的服务管理实例
func NewService() Service {
	return &linuxService{}
}

func (s *linuxService) Install(cfg ServiceConfig) error {
	// 获取原始用户（sudo 前的用户），确保服务以正确的用户身份运行
	currentUser := os.Getenv("SUDO_USER")
	if currentUser == "" {
		currentUser = os.Getenv("USER")
	}
	if currentUser == "" {
		currentUser = os.Getenv("LOGNAME")
	}

	// 通过原始用户的登录 shell 获取 HOME 和 PATH
	homeDir := getUserHome(currentUser)
	userPath := getUserPathAs(currentUser)

	unit := fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
WorkingDirectory=%s
ExecStart=%s --config %s
Restart=on-failure
RestartSec=5
KillMode=control-group
KillSignal=SIGTERM
StandardOutput=journal
StandardError=journal
Environment=PATH=%s
Environment=HOME=%s

[Install]
WantedBy=multi-user.target
`, ServiceDescription, currentUser, cfg.WorkDir, cfg.ExePath, cfg.ConfigPath, userPath, homeDir)

	if err := os.WriteFile(unitFilePath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("写入 systemd unit 文件失败: %w\n请使用 sudo 运行", err)
	}

	// systemctl daemon-reload && systemctl enable
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", ServiceName); err != nil {
		return err
	}

	fmt.Printf("服务已安装: %s\n", unitFilePath)
	fmt.Printf("使用 '%s start' 启动服务\n", filepath.Base(cfg.ExePath))
	return nil
}

func (s *linuxService) Uninstall() error {
	// 先停止服务
	systemctl("stop", ServiceName)
	systemctl("disable", ServiceName)

	if err := os.Remove(unitFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 unit 文件失败: %w", err)
	}

	systemctl("daemon-reload")
	fmt.Println("服务已卸载")
	return nil
}

func (s *linuxService) Start() error {
	if err := systemctl("start", ServiceName); err != nil {
		return err
	}
	fmt.Println("服务已启动")
	return nil
}

func (s *linuxService) Stop() error {
	if err := systemctl("stop", ServiceName); err != nil {
		return err
	}
	fmt.Println("服务已停止")
	return nil
}

func (s *linuxService) Status() (string, error) {
	out, err := exec.Command("systemctl", "status", ServiceName).CombinedOutput()
	if err != nil {
		// systemctl status 在服务未运行时返回非零退出码，这是正常的
		if len(out) > 0 {
			return parseStatus(string(out)), nil
		}
		return "未安装", nil
	}
	return parseStatus(string(out)), nil
}

func (s *linuxService) Logs(follow bool) error {
	args := []string{"-u", ServiceName, "--no-pager", "-n", "100"}
	if follow {
		args = append(args, "-f")
	}

	cmd := exec.Command("journalctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s 失败: %w", strings.Join(args, " "), err)
	}
	return nil
}

func parseStatus(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Active:") {
			return line
		}
	}
	return strings.Split(output, "\n")[0]
}

// getUserPathAs 通过指定用户的登录 shell 获取完整 PATH
func getUserPathAs(username string) string {
	// 用 su 切到原始用户执行，确保拿到该用户的 PATH 而非 root 的
	out, err := exec.Command("su", "-", username, "-c", "echo $PATH").Output()
	if err != nil {
		// 回退：直接用 bash 登录 shell
		out, err = exec.Command("bash", "-l", "-c", "echo $PATH").Output()
		if err != nil {
			return os.Getenv("PATH")
		}
	}
	return strings.TrimSpace(string(out))
}

// getUserHome 获取指定用户的 HOME 目录
func getUserHome(username string) string {
	out, err := exec.Command("getent", "passwd", username).Output()
	if err != nil {
		return os.Getenv("HOME")
	}
	// getent passwd 格式: username:x:uid:gid:comment:home:shell
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) >= 6 {
		return fields[5]
	}
	return os.Getenv("HOME")
}
