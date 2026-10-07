// Package upnptest 是测试用的假 UPnP 网关：最小的设备描述 XML + WANIPConnection 的 SOAP 控制端点，
// 可以注入常见的路由器行为（端口冲突、只支持永久映射、拒绝所有写操作、私网 WAN 地址）。
package upnptest

import (
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Mapping 是假网关上的一条端口映射。
type Mapping struct {
	Protocol       string
	ExternalPort   int
	InternalPort   int
	InternalClient string
	Description    string
	Lease          int
}

// Key 是映射在网关上的唯一键（协议 + 外部端口）。
type Key struct {
	Protocol     string
	ExternalPort int
}

// IGD 是假网关。导出字段在 Start 之后改也可以（受 mu 保护的通过方法改）。
type IGD struct {
	srv *httptest.Server

	mu            sync.Mutex
	wanIP         string
	permanentOnly bool
	denyAll       int // 非 0 时所有 AddPortMapping 回这个错误码
	reserved      map[Key]string
	mappings      map[Key]Mapping
	actions       []string // 收到的 SOAPAction 头（原样）
	remoteAddrs   []string
	v2            bool
}

// Options 配置假网关。
type Options struct {
	WANIP string
	// V2 为 true 时报 IGDv2（WANIPConnection:2）。
	V2 bool
}

// Start 起一个假网关，测试结束时关闭。
func Start(t testing.TB, opts Options) *IGD {
	t.Helper()
	if opts.WANIP == "" {
		opts.WANIP = "203.0.113.7"
	}
	g := &IGD{wanIP: opts.WANIP, v2: opts.V2, reserved: map[Key]string{}, mappings: map[Key]Mapping{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/desc.xml", g.desc)
	mux.HandleFunc("/ctl", g.control)
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

// Location 是设备描述的 URL（交给 upnp.GatewaysAt）。
func (g *IGD) Location() string { return g.srv.URL + "/desc.xml" }

// SetWANIP 改 GetExternalIPAddress 的回答。
func (g *IGD) SetWANIP(ip string) {
	g.mu.Lock()
	g.wanIP = ip
	g.mu.Unlock()
}

// SetPermanentOnly 让带租期的 AddPortMapping 回 725。
func (g *IGD) SetPermanentOnly(v bool) {
	g.mu.Lock()
	g.permanentOnly = v
	g.mu.Unlock()
}

// SetDenyAll 让所有 AddPortMapping 回 code（0 = 恢复正常）。
func (g *IGD) SetDenyAll(code int) {
	g.mu.Lock()
	g.denyAll = code
	g.mu.Unlock()
}

// Reserve 让 proto/external 已被另一个内部地址占用（对我们回 718）。
func (g *IGD) Reserve(proto string, external int) {
	g.mu.Lock()
	g.reserved[Key{proto, external}] = "192.0.2.99"
	g.mu.Unlock()
}

// Mappings 返回当前映射的快照。
func (g *IGD) Mappings() map[Key]Mapping {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[Key]Mapping, len(g.mappings))
	for k, v := range g.mappings {
		out[k] = v
	}
	return out
}

// Clear 模拟路由器重启丢掉全部映射。
func (g *IGD) Clear() {
	g.mu.Lock()
	g.mappings = map[Key]Mapping{}
	g.mu.Unlock()
}

// Actions 返回收到的 SOAPAction 头（原样）。
func (g *IGD) Actions() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.actions...)
}

// RemoteAddrs 返回 SOAP 请求的来源地址。
func (g *IGD) RemoteAddrs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.remoteAddrs...)
}

func (g *IGD) urn() string {
	if g.v2 {
		return "urn:schemas-upnp-org:service:WANIPConnection:2"
	}
	return "urn:schemas-upnp-org:service:WANIPConnection:1"
}

func (g *IGD) desc(w http.ResponseWriter, _ *http.Request) {
	v := "1"
	if g.v2 {
		v = "2"
	}
	fmt.Fprintf(w, `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
 <device>
  <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:%[1]s</deviceType>
  <friendlyName>Fake IGD</friendlyName>
  <deviceList><device>
   <deviceType>urn:schemas-upnp-org:device:WANDevice:%[1]s</deviceType>
   <deviceList><device>
    <deviceType>urn:schemas-upnp-org:device:WANConnectionDevice:%[1]s</deviceType>
    <serviceList><service>
     <serviceType>%[2]s</serviceType>
     <serviceId>urn:upnp-org:serviceId:WANIPConn1</serviceId>
     <controlURL>/ctl</controlURL>
    </service></serviceList>
   </device></deviceList>
  </device></deviceList>
 </device>
</root>`, v, g.urn())
}

type addArgs struct {
	ExternalPort   int    `xml:"Body>AddPortMapping>NewExternalPort"`
	Protocol       string `xml:"Body>AddPortMapping>NewProtocol"`
	InternalPort   int    `xml:"Body>AddPortMapping>NewInternalPort"`
	InternalClient string `xml:"Body>AddPortMapping>NewInternalClient"`
	Description    string `xml:"Body>AddPortMapping>NewPortMappingDescription"`
	Lease          int    `xml:"Body>AddPortMapping>NewLeaseDuration"`
}

type delArgs struct {
	ExternalPort int    `xml:"Body>DeletePortMapping>NewExternalPort"`
	Protocol     string `xml:"Body>DeletePortMapping>NewProtocol"`
}

func (g *IGD) control(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	// net/http 服务端会把头名规范化成 Soapaction，所以这里验不了「原样大小写」，只验值的格式。
	action := r.Header.Get("SOAPAction")
	g.mu.Lock()
	g.actions = append(g.actions, action)
	g.remoteAddrs = append(g.remoteAddrs, r.RemoteAddr)
	g.mu.Unlock()
	fn := strings.Trim(action[strings.LastIndex(action, "#")+1:], `"`)
	switch fn {
	case "GetExternalIPAddress":
		g.mu.Lock()
		ip := g.wanIP
		g.mu.Unlock()
		g.ok(w, fn, "<NewExternalIPAddress>"+ip+"</NewExternalIPAddress>")
	case "AddPortMapping":
		var a addArgs
		if err := xml.Unmarshal(body, &a); err != nil {
			g.fault(w, 402, "Invalid Args")
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		k := Key{a.Protocol, a.ExternalPort}
		switch {
		case g.denyAll != 0:
			g.fault(w, g.denyAll, "Denied")
		case g.permanentOnly && a.Lease > 0:
			g.fault(w, 725, "OnlyPermanentLeasesSupported")
		case g.reserved[k] != "" && g.reserved[k] != a.InternalClient:
			g.fault(w, 718, "ConflictInMappingEntry")
		case g.mappings[k].InternalClient != "" && g.mappings[k].InternalClient != a.InternalClient:
			g.fault(w, 718, "ConflictInMappingEntry")
		default:
			g.mappings[k] = Mapping{Protocol: a.Protocol, ExternalPort: a.ExternalPort, InternalPort: a.InternalPort,
				InternalClient: a.InternalClient, Description: a.Description, Lease: a.Lease}
			g.ok(w, fn, "")
		}
	case "DeletePortMapping":
		var a delArgs
		if err := xml.Unmarshal(body, &a); err != nil {
			g.fault(w, 402, "Invalid Args")
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		k := Key{a.Protocol, a.ExternalPort}
		if _, ok := g.mappings[k]; !ok {
			g.fault(w, 714, "NoSuchEntryInArray")
			return
		}
		delete(g.mappings, k)
		g.ok(w, fn, "")
	default:
		g.fault(w, 401, "Invalid Action")
	}
}

func (g *IGD) ok(w http.ResponseWriter, fn, inner string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	fmt.Fprintf(w, `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><u:%[1]sResponse xmlns:u="%[2]s">%[3]s</u:%[1]sResponse></s:Body></s:Envelope>`, fn, g.urn(), inner)
}

func (g *IGD) fault(w http.ResponseWriter, code int, desc string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail>
<UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%d</errorCode><errorDescription>%s</errorDescription></UPnPError>
</detail></s:Fault></s:Body></s:Envelope>`, code, desc)
}

// LoopbackIP 是测试里的「本机内部地址」。
var LoopbackIP = net.IPv4(127, 0, 0, 1)
