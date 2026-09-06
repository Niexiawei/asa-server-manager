//go:build !windows && !(linux && amd64)

package procnet

// 既不是 Windows，也不是 linux/amd64：
//   - Linux 的非 amd64（arm64 等）——本项目整体只支持 amd64（ASA 专用服务器在
//     arm64 上跑不起来），所以不生成也不提交 arm64 的 BPF 产物，走 stub 即可
//     （docs/RESOURCE_RATE_CHART_PLAN.md 决策 22）。
//   - 其它 UNIX（darwin 等）——只是为了 go build 能过，本项目并不在其上运行。

// Collector 是个空壳，只为让调用方无需 build tag 就能编译。
type Collector struct{}

// Load 恒返回 ErrUnsupported。
func Load(Options) (*Collector, error) { return nil, ErrUnsupported }

// Bytes 恒返回 ok=false。
func (c *Collector) Bytes(int32) (rx, tx uint64, ok bool) { return 0, 0, false }

// Describe 恒为空。
func (c *Collector) Describe() string { return "" }

// Close 无事可做。
func (c *Collector) Close() error { return nil }
