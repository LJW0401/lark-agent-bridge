//go:build windows

package platform

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type windowsService struct{}

// NewService 创建 Windows 平台的服务管理实例
func NewService() Service {
	return &windowsService{}
}

func (s *windowsService) Install(cfg ServiceConfig) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败: %w\n请以管理员身份运行", err)
	}
	defer m.Disconnect()

	// 检查是否已安装
	existingSvc, err := m.OpenService(ServiceName)
	if err == nil {
		existingSvc.Close()
		return fmt.Errorf("服务 %s 已存在，请先卸载", ServiceName)
	}

	svcConfig := mgr.Config{
		DisplayName:  ServiceDisplayName,
		Description:  ServiceDescription,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}

	args := []string{"--config", cfg.ConfigPath}
	newSvc, err := m.CreateService(ServiceName, cfg.ExePath, svcConfig, args...)
	if err != nil {
		return fmt.Errorf("创建服务失败: %w", err)
	}
	defer newSvc.Close()

	// 设置故障恢复策略：失败后自动重启
	recoveryActions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := newSvc.SetRecoveryActions(recoveryActions, 86400); err != nil {
		// 恢复策略设置失败不影响安装
		fmt.Printf("警告: 设置故障恢复策略失败: %v\n", err)
	}

	fmt.Printf("服务已安装: %s\n", ServiceDisplayName)
	fmt.Println("使用 'lark-agent-bridge start' 启动服务")
	return nil
}

func (s *windowsService) Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败: %w", err)
	}
	defer m.Disconnect()

	sv, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("服务 %s 不存在: %w", ServiceName, err)
	}
	defer sv.Close()

	// 先尝试停止
	sv.Control(svc.Stop)
	time.Sleep(2 * time.Second)

	if err := sv.Delete(); err != nil {
		return fmt.Errorf("删除服务失败: %w", err)
	}

	fmt.Println("服务已卸载")
	return nil
}

func (s *windowsService) Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败: %w", err)
	}
	defer m.Disconnect()

	sv, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("服务 %s 不存在: %w", ServiceName, err)
	}
	defer sv.Close()

	if err := sv.Start(); err != nil {
		return fmt.Errorf("启动服务失败: %w", err)
	}

	fmt.Println("服务已启动")
	return nil
}

func (s *windowsService) Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败: %w", err)
	}
	defer m.Disconnect()

	sv, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("服务 %s 不存在: %w", ServiceName, err)
	}
	defer sv.Close()

	status, err := sv.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("停止服务失败: %w", err)
	}

	// 等待服务停止
	timeout := time.After(30 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("等待服务停止超时")
		case <-tick.C:
			status, err = sv.Query()
			if err != nil {
				return fmt.Errorf("查询服务状态失败: %w", err)
			}
			if status.State == svc.Stopped {
				fmt.Println("服务已停止")
				return nil
			}
		}
	}
}

func (s *windowsService) Status() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("连接服务管理器失败: %w", err)
	}
	defer m.Disconnect()

	sv, err := m.OpenService(ServiceName)
	if err != nil {
		return "未安装", nil
	}
	defer sv.Close()

	status, err := sv.Query()
	if err != nil {
		return "", fmt.Errorf("查询服务状态失败: %w", err)
	}

	switch status.State {
	case svc.Stopped:
		return "已停止", nil
	case svc.StartPending:
		return "正在启动", nil
	case svc.StopPending:
		return "正在停止", nil
	case svc.Running:
		return "运行中", nil
	case svc.ContinuePending:
		return "正在恢复", nil
	case svc.PausePending:
		return "正在暂停", nil
	case svc.Paused:
		return "已暂停", nil
	default:
		return fmt.Sprintf("未知状态 (%d)", status.State), nil
	}
}

func (s *windowsService) Logs(follow bool) error {
	// Windows 通过 Event Viewer 查看日志，这里用 PowerShell 获取最近日志
	args := []string{
		"-NoProfile", "-Command",
		fmt.Sprintf(`Get-WinEvent -FilterHashtable @{LogName='Application';ProviderName='%s'} -MaxEvents 100 | Format-Table -Wrap`, ServiceName),
	}

	cmd := exec.Command("powershell.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// 没有日志记录时 PowerShell 会报错
		if strings.Contains(string(out), "No events were found") {
			fmt.Println("暂无日志记录。日志文件位置请查看 config.yaml 中的 log.file 配置。")
			return nil
		}
		// 回退到提示查看日志文件
		fmt.Println("无法从 Windows 事件日志读取，请直接查看日志文件。")
		fmt.Println("日志路径可在 config.yaml 的 log.file 中查看。")
		return nil
	}

	fmt.Println(string(out))
	return nil
}
