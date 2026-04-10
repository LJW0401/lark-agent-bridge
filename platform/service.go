package platform

// ServiceConfig 服务安装所需的配置
type ServiceConfig struct {
	// 可执行文件绝对路径
	ExePath string
	// 配置文件绝对路径
	ConfigPath string
	// 工作目录
	WorkDir string
	// 服务描述
	Description string
}

const (
	ServiceName        = "lark-agent-bridge"
	ServiceDisplayName = "Lark Agent Bridge"
	ServiceDescription = "飞书 AI Agent 消息桥接服务"
)

// Service 跨平台服务管理接口
type Service interface {
	// Install 安装为系统服务
	Install(cfg ServiceConfig) error
	// Uninstall 卸载系统服务
	Uninstall() error
	// Start 启动服务
	Start() error
	// Stop 停止服务
	Stop() error
	// Status 查询服务状态
	Status() (string, error)
	// Logs 查看服务日志（仅 Linux 支持 follow）
	Logs(follow bool) error
}
