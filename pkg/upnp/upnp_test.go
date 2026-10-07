package upnp_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"asa-server/pkg/upnp"
	"asa-server/pkg/upnp/upnptest"
)

func gateway(t *testing.T, g *upnptest.IGD) *upnp.Gateway {
	t.Helper()
	gws, err := upnp.GatewaysAt(context.Background(), g.Location(), upnptest.LoopbackIP)
	if err != nil || len(gws) != 1 {
		t.Fatalf("读设备描述失败：%v %v", gws, err)
	}
	return gws[0]
}

func TestGatewayLifecycle(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		g := upnptest.Start(t, upnptest.Options{V2: v2})
		gw := gateway(t, g)
		if gw.IsV2() != v2 || gw.FriendlyName != "Fake IGD" || !strings.HasSuffix(gw.URL, "/ctl") {
			t.Fatalf("服务描述不对：%+v", gw)
		}
		ctx := context.Background()
		ip, err := gw.GetExternalIPv4Address(ctx)
		if err != nil || ip.String() != "203.0.113.7" {
			t.Fatalf("WAN 地址：%v %v", ip, err)
		}
		lease, err := gw.AddPortMapping(ctx, upnp.TCP, 19194, 19194, "asa-server mesh <t&t>", time.Hour)
		if err != nil || lease != time.Hour {
			t.Fatalf("添加映射：%v %v", lease, err)
		}
		m := g.Mappings()[upnptest.Key{Protocol: "TCP", ExternalPort: 19194}]
		if m.InternalClient != "127.0.0.1" || m.InternalPort != 19194 || m.Lease != 3600 || m.Description != "asa-server mesh <t&t>" {
			t.Fatalf("网关上的映射不对（描述要转义后原样还原）：%+v", m)
		}
		if err := gw.DeletePortMapping(ctx, upnp.TCP, 19194); err != nil || len(g.Mappings()) != 0 {
			t.Fatalf("删除映射：%v %v", err, g.Mappings())
		}

		// SOAPAction 的值是带引号的「服务 URN#动作」；SOAP 请求的源地址绑到 InternalClient。
		for _, a := range g.Actions() {
			if !strings.HasPrefix(a, `"urn:schemas-upnp-org:service:WANIPConnection:`) || !strings.HasSuffix(a, `"`) {
				t.Fatalf("SOAPAction 头不对：%q", a)
			}
		}
		for _, ra := range g.RemoteAddrs() {
			if host, _, _ := net.SplitHostPort(ra); host != "127.0.0.1" {
				t.Fatalf("SOAP 请求的来源地址不对：%s", ra)
			}
		}
	}
}

func TestGatewayErrors(t *testing.T) {
	g := upnptest.Start(t, upnptest.Options{})
	gw := gateway(t, g)
	ctx := context.Background()

	g.Reserve("UDP", 19194)
	_, err := gw.AddPortMapping(ctx, upnp.UDP, 19194, 19194, "x", time.Hour)
	var se *upnp.SOAPError
	if !errors.As(err, &se) || se.Code != upnp.ErrCodeConflict {
		t.Fatalf("被占的端口应回 718：%v", err)
	}

	// 只支持永久映射：自动以租期 0 重试，返回实际给到的租期。
	g.SetPermanentOnly(true)
	lease, err := gw.AddPortMapping(ctx, upnp.TCP, 1000, 2000, "x", time.Hour)
	if err != nil || lease != 0 || g.Mappings()[upnptest.Key{Protocol: "TCP", ExternalPort: 2000}].Lease != 0 {
		t.Fatalf("725 应回退到永久映射：%v %v", lease, err)
	}

	g.SetDenyAll(606)
	if _, err := gw.AddPortMapping(ctx, upnp.TCP, 1000, 3000, "x", time.Hour); !errors.As(err, &se) || se.Code != 606 {
		t.Fatalf("应原样带回错误码：%v", err)
	}
	if err := gw.DeletePortMapping(ctx, upnp.TCP, 4000); !errors.As(err, &se) || se.Code != 714 {
		t.Fatalf("删除不存在的映射应回 714：%v", err)
	}
}

func TestGatewaysAtRejectsNonIGD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.xml" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<root><device><deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType></device></root>`))
	}))
	defer srv.Close()
	ctx := context.Background()
	if _, err := upnp.GatewaysAt(ctx, srv.URL+"/desc.xml", upnptest.LoopbackIP); err == nil {
		t.Fatal("不是 IGD 的设备描述应报错")
	}
	if _, err := upnp.GatewaysAt(ctx, srv.URL+"/missing.xml", upnptest.LoopbackIP); err == nil {
		t.Fatal("取不到设备描述应报错")
	}
}
