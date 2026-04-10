//go:build windows

package platform

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc"
)

// IsWindowsService 检测当前进程是否作为 Windows Service 运行
func IsWindowsService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return isService
}

// RunAsService 以 Windows Service 模式运行，bridgeMain 是实际的业务逻辑
func RunAsService(bridgeMain func(stop <-chan struct{})) error {
	return svc.Run(ServiceName, &bridgeHandler{main: bridgeMain})
}

type bridgeHandler struct {
	main func(stop <-chan struct{})
}

func (h *bridgeHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	stopCh := make(chan struct{})

	go func() {
		h.main(stopCh)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		c := <-r
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			close(stopCh)
			return false, 0
		default:
			fmt.Fprintf(os.Stderr, "未知的服务控制请求: %d\n", c.Cmd)
		}
	}
}
