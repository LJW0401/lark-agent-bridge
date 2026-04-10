#!/usr/bin/env bash
# lark-agent-bridge 一键安装脚本
# 用法: curl -fsSL https://xxx/install.sh | bash
#   或: ./install.sh [--no-service]
set -e

BINARY_NAME="lark-agent-bridge"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/lark-agent-bridge"
REPO="LJW0401/lark-agent-bridge"
NO_SERVICE=false

# 解析参数
for arg in "$@"; do
    case "$arg" in
        --no-service) NO_SERVICE=true ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo "选项:"
            echo "  --no-service   仅安装二进制，不注册系统服务"
            echo "  --help         显示此帮助信息"
            exit 0
            ;;
    esac
done

# --- 工具函数 ---

info()  { echo -e "\033[32m[INFO]\033[0m $*"; }
warn()  { echo -e "\033[33m[WARN]\033[0m $*"; }
error() { echo -e "\033[31m[ERROR]\033[0m $*"; exit 1; }

check_cmd() {
    command -v "$1" &>/dev/null
}

detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) error "不支持的架构: $arch" ;;
    esac
}

detect_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        linux) echo "linux" ;;
        *) error "此脚本仅支持 Linux，当前系统: $os" ;;
    esac
}

# --- 检查前置依赖 ---

check_dependencies() {
    info "检查依赖..."

    if ! check_cmd lark-cli; then
        warn "lark-cli 未安装"
        echo "  请先安装: npm install -g @larksuite/cli"
        echo "  然后运行: lark-cli config init && lark-cli auth login --recommend"
        exit 1
    fi
    info "lark-cli: $(which lark-cli)"

    # 检查 AI Agent（至少一个）
    local has_agent=false
    if check_cmd codex; then
        info "codex: $(which codex)"
        has_agent=true
    fi
    if check_cmd claude; then
        info "claude: $(which claude)"
        has_agent=true
    fi
    if ! $has_agent; then
        warn "未检测到 codex 或 claude，请确保至少安装一个 AI Agent"
    fi
}

# --- 下载或本地安装 ---

install_binary() {
    local arch os
    arch=$(detect_arch)
    os=$(detect_os)

    # 如果当前目录有编译好的二进制，直接用
    if [[ -f "./build/${BINARY_NAME}" ]]; then
        info "使用本地编译的二进制: ./build/${BINARY_NAME}"
        sudo cp "./build/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
        sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
        return
    fi

    # 从 GitHub Release 下载
    info "下载 ${BINARY_NAME} (${os}/${arch})..."

    local download_url
    download_url=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep "browser_download_url" \
        | grep "${os}_${arch}" \
        | head -1 \
        | cut -d'"' -f4)

    if [[ -z "$download_url" ]]; then
        error "未找到适合 ${os}/${arch} 的发布版本，请手动编译安装"
    fi

    local tmpfile
    tmpfile=$(mktemp /tmp/${BINARY_NAME}.XXXXXX)
    curl -fsSL "$download_url" -o "$tmpfile"
    sudo mv "$tmpfile" "${INSTALL_DIR}/${BINARY_NAME}"
    sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    info "已安装到: ${INSTALL_DIR}/${BINARY_NAME}"
}

# --- 配置 ---

setup_config() {
    sudo mkdir -p "$CONFIG_DIR"

    if [[ -f "${CONFIG_DIR}/config.yaml" ]]; then
        info "配置文件已存在: ${CONFIG_DIR}/config.yaml"
        return
    fi

    # 复制模板
    if [[ -f "./config.example.yaml" ]]; then
        sudo cp "./config.example.yaml" "${CONFIG_DIR}/config.yaml"
    fi

    info "运行配置向导..."
    sudo "${INSTALL_DIR}/${BINARY_NAME}" config init
}

# --- 注册服务 ---

setup_service() {
    if $NO_SERVICE; then
        info "跳过服务注册 (--no-service)"
        return
    fi

    info "注册系统服务..."
    sudo "${INSTALL_DIR}/${BINARY_NAME}" install
    echo ""
    info "启动服务: sudo ${BINARY_NAME} start"
    info "查看状态: sudo ${BINARY_NAME} status"
    info "查看日志: sudo ${BINARY_NAME} logs -f"
}

# --- 卸载 ---

uninstall() {
    info "卸载 ${BINARY_NAME}..."
    sudo "${INSTALL_DIR}/${BINARY_NAME}" uninstall 2>/dev/null || true
    sudo rm -f "${INSTALL_DIR}/${BINARY_NAME}"
    info "二进制已删除"
    echo "配置文件保留在: ${CONFIG_DIR}/"
    echo "如需彻底删除: sudo rm -rf ${CONFIG_DIR}"
}

# --- 主流程 ---

main() {
    echo "=== ${BINARY_NAME} 安装程序 ==="
    echo ""

    if [[ "${1:-}" == "uninstall" ]]; then
        uninstall
        exit 0
    fi

    check_dependencies
    echo ""
    install_binary
    echo ""
    setup_config
    echo ""
    setup_service
    echo ""
    info "安装完成！"
    echo ""
    "${INSTALL_DIR}/${BINARY_NAME}" version
}

main "$@"
