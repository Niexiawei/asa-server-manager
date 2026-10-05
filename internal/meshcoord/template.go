package meshcoord

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"asa-server/pkg/atomicfile"
)

// configTemplate 是 config init 生成的模板：只有一份，注释用中文（协调节点由运营者部署，
// 不像 asa-server 那样维护中英两份）。UTF-8 无 BOM、LF——目标平台是 Linux。
//
// 模板必须能被原样加载并通过校验（有单测守着）；public_addr 用文档示例地址占位，
// 校验时会给出「还是示例地址」的警告。
const configTemplate = `# asa-coordinator 配置文件
# 文档：docs/REMOTE_MANAGER_MESH_PLAN.md §8、§12 P1-5
#
# 防火墙 / 云安全组需要放行：
#   - TCP：listen 的端口（默认 443）——管理器的长连接与中转都走它
#   - UDP：stun.listen 的两个端口（默认 3478、3479）——这一步最容易漏

# gRPC（TLS）监听地址
listen: ":443"

# 管理器连接用的地址（本机的公网 IP 或域名 + 端口），会写进 join blob。
# ← 必须改成你自己的地址，下面是文档示例地址
public_addr: "203.0.113.10:443"

# 数据目录：数据库与本节点的身份。相对路径按本文件所在目录解析。
data_dir: "data"

# 正式证书（前面挂了域名时可用）。留空 = 使用 data_dir 里的自签证书。
# 两种情况下 join blob 里写的都是证书公钥的指纹，管理器按它钉住本节点。
tls:
  cert_file: ""
  key_file: ""

# 中转限额：中转是协调节点唯一的成本项
limits:
  # 每个节点同时参与的中转会话上限
  max_relays_per_node: 8
  # 中转会话多久没有数据就关闭
  relay_idle_timeout: 5m
  # 每个节点经中转发出的速率上限（KiB/s），0 = 不限
  relay_rate_kbps: 0

# STUN 端点（标准 STUN Binding，可直接用现成的 STUN 工具排障）。
# listen 留空 = 关闭；配两个端口才能让管理器分辨出对称型 NAT。
stun:
  listen: [":3478", ":3479"]
  # 下发给管理器的地址；留空 = public_addr 的主机名 + 上面各端口
  advertise: []
  # 每个来源（IPv4 按单个地址、IPv6 按 /64）的限流
  rate_per_source: 5
  burst_per_source: 10
  # 全局限流（次/秒）
  rate_global: 2000
`

// ErrConfigExists 表示目标已存在且没有 --force。
var ErrConfigExists = errors.New("配置文件已存在")

// DefaultInitPath 是 config init 不带 -o 时的输出位置：二进制同目录。
func DefaultInitPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), ConfigFileName), nil
}

// InitConfig 把模板写到 path。目标已存在时拒绝；force 时先备份成
// <path>.bak-<时间戳> 再覆盖。原子写。返回备份文件路径（没有备份时为空）。
func InitConfig(path string, force bool) (backup string, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !force {
			return "", fmt.Errorf("%w：%s（加 --force 覆盖，旧文件会先备份）", ErrConfigExists, path)
		}
		backup = path + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", fmt.Errorf("备份 %s 失败: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	if err := atomicfile.Write(path, []byte(configTemplate), 0o644); err != nil {
		return backup, err
	}
	return backup, nil
}
