//go:build linux

package platform

// IsWindowsService Linux 上始终返回 false
func IsWindowsService() bool {
	return false
}

// RunAsService Linux 上不需要特殊处理，直接调用 bridgeMain
func RunAsService(bridgeMain func(stop <-chan struct{})) error {
	stopCh := make(chan struct{})
	bridgeMain(stopCh)
	return nil
}
