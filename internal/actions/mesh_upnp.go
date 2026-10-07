package actions

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/urfave/cli/v3"

	"asa-server/internal/mesh"
	"asa-server/pkg/upnp"
)

// actionMeshUPnP 是 UPnP 的排查入口（§12「P6 补充：UPnP 端口映射」U-6）：UPnP 出问题几乎都是路由器设置，
// 用户需要一个不依赖服务的检查。不碰正在运行的服务的映射（--test 用随机外部端口）。
func actionMeshUPnP(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("正在查找支持 UPnP 的路由器（约 3 秒）…")
	gws := upnp.Discover(ctx, 3*time.Second)
	if len(gws) == 0 {
		fmt.Println("未发现支持 UPnP 的路由器。可能的原因：路由器没有开启 UPnP；本机防火墙挡住了 SSDP 的回包（UDP 1900）；")
		fmt.Println("本机不在路由器后面（例如云服务器，那本来也不需要映射）。")
		return nil
	}
	var firstOK *upnp.Gateway
	for i, g := range gws {
		fmt.Printf("\n[%d] %s\n", i+1, g.FriendlyName)
		fmt.Printf("    服务：    %s\n", g.URN)
		fmt.Printf("    本机地址：%s（网卡 %s）\n", g.LocalIPv4, g.Interface)
		fmt.Printf("    控制地址：%s\n", g.URL)
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ip, err := g.GetExternalIPv4Address(cctx)
		cancel()
		if err != nil {
			fmt.Printf("    WAN 地址：读取失败：%v\n", err)
			continue
		}
		fmt.Printf("    WAN 地址：%s\n", ip)
		if ok, reason := mesh.UPnPVerdict(ip); ok {
			fmt.Println("    结论：    公网地址，映射后对方可以直接连进来")
			if firstOK == nil {
				firstOK = g
			}
		} else {
			fmt.Printf("    结论：    %s\n", reason)
		}
	}
	if !cmd.Bool("test") {
		return nil
	}
	g := firstOK
	if g == nil {
		g = gws[0]
	}
	port := 20000 + rand.IntN(40000)
	fmt.Printf("\n试加一条临时映射：TCP %d → %s:%d（60 秒）…\n", port, g.LocalIPv4, port)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lease, err := g.AddPortMapping(cctx, upnp.TCP, port, port, "asa-server mesh upnp test", time.Minute)
	if err != nil {
		var se *upnp.SOAPError
		if errors.As(err, &se) && se.Code == 606 {
			return fmt.Errorf("路由器拒绝了映射请求（606：可能开启了 UPnP 的安全模式，只允许特定设备）：%w", err)
		}
		return fmt.Errorf("添加映射失败：%w", err)
	}
	if lease == 0 {
		fmt.Println("成功（路由器只支持永久映射；mesh 停止时会删除它的映射）")
	} else {
		fmt.Println("成功")
	}
	if err := g.DeletePortMapping(cctx, upnp.TCP, port); err != nil {
		return fmt.Errorf("删除临时映射失败（请到路由器管理页手动删除 TCP %d）：%w", port, err)
	}
	fmt.Println("已删除临时映射。")
	return nil
}
