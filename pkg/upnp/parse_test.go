package upnp

import (
	"errors"
	"net"
	"net/url"
	"testing"
)

func TestParseSSDP(t *testing.T) {
	resp := []byte("HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age=120\r\n" +
		"ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n" +
		"USN: uuid:abcd-1234::urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n" +
		"LOCATION: http://192.168.1.1:1900/igd.xml\r\n\r\n")
	r, err := parseSSDP(urnIgdV1, resp)
	if err != nil || r.location != "http://192.168.1.1:1900/igd.xml" || r.uuid != "abcd-1234" {
		t.Fatalf("解析不对：%+v %v", r, err)
	}
	var unsupp *UnsupportedDeviceTypeError
	if _, err := parseSSDP(urnIgdV2, resp); !errors.As(err, &unsupp) {
		t.Fatalf("ST 不符应报 UnsupportedDeviceTypeError：%v", err)
	}
	noLoc := []byte("HTTP/1.1 200 OK\r\nST: " + urnIgdV1 + "\r\nUSN: uuid:x\r\n\r\n")
	if _, err := parseSSDP(urnIgdV1, noLoc); err == nil {
		t.Fatal("没有 LOCATION 应报错")
	}
}

// PPPoE 拨号的路由器只有 WANPPPConnection；controlURL 有相对、绝对两种写法。
func TestServiceDescriptions(t *testing.T) {
	dev := upnpDevice{
		DeviceType:   urnIgdV1,
		FriendlyName: "Router",
		Devices: []upnpDevice{{
			DeviceType: urnWANDeviceV1,
			Devices: []upnpDevice{{
				DeviceType: urnWANConnectionDeviceV1,
				Services: []upnpService{
					{ID: "ppp", Type: urnWANPPPConnectionV1, ControlURL: "ctl/ppp?x=1"},
					{ID: "ip", Type: urnWANIPConnectionV1, ControlURL: "http://10.0.0.1:5000/upnp/ip"},
					{ID: "other", Type: "urn:schemas-upnp-org:service:Layer3Forwarding:1", ControlURL: "/l3f"},
				},
			}},
		}},
	}
	gws, err := getServiceDescriptions("u", net.IPv4(192, 168, 1, 5), "http://192.168.1.1:1900/root/", dev, "eth0")
	if err != nil || len(gws) != 2 {
		t.Fatalf("应找到 WANIP 与 WANPPP 两个服务：%v %v", gws, err)
	}
	urls := map[string]string{}
	for _, g := range gws {
		urls[g.ServiceID] = g.URL
		if g.FriendlyName != "Router" || g.Interface != "eth0" || !g.LocalIPv4.Equal(net.IPv4(192, 168, 1, 5)) {
			t.Fatalf("服务字段不对：%+v", g)
		}
	}
	if urls["ppp"] != "http://192.168.1.1:1900/root/ctl/ppp?x=1" || urls["ip"] != "http://192.168.1.1:1900/upnp/ip" {
		t.Fatalf("controlURL 拼接不对：%v", urls)
	}

	if _, err := getServiceDescriptions("u", nil, "http://x/", upnpDevice{DeviceType: "urn:other"}, ""); err == nil {
		t.Fatal("不是 IGD 应报错")
	}
}

func TestReplaceRawPath(t *testing.T) {
	for _, c := range []struct{ root, ctl, want string }{
		{"http://h:1/a/b.xml", "/ctl", "http://h:1/ctl"},
		{"http://h:1/a/", "ctl?q=1", "http://h:1/a/ctl?q=1"},
		{"http://h:1/a/", "http://other:2/x?y", "http://h:1/x?y"},
	} {
		u, _ := url.Parse(c.root)
		replaceRawPath(u, c.ctl)
		if u.String() != c.want {
			t.Errorf("%s + %s = %s，期望 %s", c.root, c.ctl, u, c.want)
		}
	}
}

func TestSOAPErrorParsing(t *testing.T) {
	body := []byte(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault>
<detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>718</errorCode><errorDescription>ConflictInMappingEntry</errorDescription></UPnPError></detail>
</s:Fault></s:Body></s:Envelope>`)
	var se *SOAPError
	if err := soapError("AddPortMapping", "500", body); !errors.As(err, &se) || se.Code != 718 || se.Description != "ConflictInMappingEntry" {
		t.Fatalf("SOAP 错误解析不对：%v", err)
	}
	if err := soapError("AddPortMapping", "500 Internal", []byte("garbage")); errors.As(err, &se) {
		t.Fatalf("没有 UPnPError 时不该是 SOAPError：%v", err)
	}
}
