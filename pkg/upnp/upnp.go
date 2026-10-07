// Copyright (C) 2014 The Syncthing Authors.
//
// Adapted from https://github.com/jackpal/Taipei-Torrent/blob/dd88a8bfac6431c01d959ce3c745e74b8a911793/IGD.go
// Copyright (c) 2010 Jack Palevich (https://github.com/jackpal/Taipei-Torrent/blob/dd88a8bfac6431c01d959ce3c745e74b8a911793/LICENSE)
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
//    * Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//    * Redistributions in binary form must reproduce the above
// copyright notice, this list of conditions and the following disclaimer
// in the documentation and/or other materials provided with the
// distribution.
//    * Neither the name of Google Inc. nor the names of its
// contributors may be used to endorse or promote products derived from
// this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

// Modified for asa-server (2026-10): taken from Syncthing lib/upnp/upnp.go
// (commit 2ca95cf1). Changes: no dependency on Syncthing's lib/nat, lib/build,
// lib/dialer, lib/osutil, lib/netutil or slog helpers; IPv4 only (the IPv6
// SSDP discovery and WANIPv6FirewallControl pinholes were removed); Discover
// returns *Gateway directly; GatewaysAt was split out of parseResponse so that
// a known device description can be used without SSDP; SOAP faults are
// returned as *SOAPError. See docs/REMOTE_MANAGER_MESH_PLAN.md §12 "P6 补充".

// Package upnp implements UPnP InternetGatewayDevice discovery, querying, and port mapping.
//
// 只做 IPv4 端口映射（WANIPConnection / WANPPPConnection，IGDv1 与 IGDv2）。不认识任何领域概念，
// 续约、选端口、何时删除由调用方决定。
package upnp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"asa-server/pkg/logger"
)

// userAgent 随 SSDP 与 SOAP 请求发出。
const userAgent = "upnp-client/1.0 UPnP/1.1"

type upnpService struct {
	ID         string `xml:"serviceId"`
	Type       string `xml:"serviceType"`
	ControlURL string `xml:"controlURL"`
}

type upnpDevice struct {
	DeviceType   string        `xml:"deviceType"`
	FriendlyName string        `xml:"friendlyName"`
	Devices      []upnpDevice  `xml:"deviceList>device"`
	Services     []upnpService `xml:"serviceList>service"`
}

type upnpRoot struct {
	Device upnpDevice `xml:"device"`
}

// UnsupportedDeviceTypeError for unsupported UPnP device types (i.e upnp:rootdevice)
type UnsupportedDeviceTypeError struct {
	deviceType string
}

func (e *UnsupportedDeviceTypeError) Error() string {
	return "unsupported UPnP device of type " + e.deviceType
}

const (
	urnIgdV1                 = "urn:schemas-upnp-org:device:InternetGatewayDevice:1"
	urnIgdV2                 = "urn:schemas-upnp-org:device:InternetGatewayDevice:2"
	urnWANDeviceV1           = "urn:schemas-upnp-org:device:WANDevice:1"
	urnWANDeviceV2           = "urn:schemas-upnp-org:device:WANDevice:2"
	urnWANConnectionDeviceV1 = "urn:schemas-upnp-org:device:WANConnectionDevice:1"
	urnWANConnectionDeviceV2 = "urn:schemas-upnp-org:device:WANConnectionDevice:2"
	urnWANIPConnectionV1     = "urn:schemas-upnp-org:service:WANIPConnection:1"
	urnWANIPConnectionV2     = "urn:schemas-upnp-org:service:WANIPConnection:2"
	urnWANPPPConnectionV1    = "urn:schemas-upnp-org:service:WANPPPConnection:1"
	urnWANPPPConnectionV2    = "urn:schemas-upnp-org:service:WANPPPConnection:2"
)

// Discover discovers UPnP InternetGatewayDevices.
// The order in which the devices appear in the results list is not deterministic.
func Discover(ctx context.Context, timeout time.Duration) []*Gateway {
	var results []*Gateway

	interfaces, err := net.Interfaces()
	if err != nil {
		logger.Warnf("[upnp] 枚举网卡失败: %v", err)
		return results
	}

	resultChan := make(chan *Gateway)

	wg := &sync.WaitGroup{}

	for _, intf := range interfaces {
		if intf.Flags&net.FlagRunning == 0 || intf.Flags&net.FlagMulticast == 0 {
			continue
		}

		// Discovery is done sequentially per interface because we discovered that
		// FritzBox routers return a broken result sometimes if the IPv4 and IPv6
		// request arrive at the same time.
		wg.Go(func() {
			for _, deviceType := range []string{urnIgdV2, urnIgdV1} {
				discover(ctx, &intf, deviceType, timeout, resultChan)
			}
		})
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	seenResults := make(map[string]bool)
	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				return results
			}
			if seenResults[result.ID()] {
				logger.Debugf("[upnp] Skipping duplicate result %s", result.ID())
				continue
			}

			results = append(results, result)
			seenResults[result.ID()] = true

			logger.Debugf("[upnp] UPnP discovery result %s", result.ID())
		case <-ctx.Done():
			return nil
		}
	}
}

// Search for UPnP InternetGatewayDevices for <timeout> seconds.
// The order in which the devices appear in the result list is not deterministic
func discover(ctx context.Context, intf *net.Interface, deviceType string, timeout time.Duration, results chan<- *Gateway) {
	ssdp := net.UDPAddr{IP: []byte{239, 255, 255, 250}, Port: 1900}

	const template = `M-SEARCH * HTTP/1.1
HOST: 239.255.255.250:1900
ST: %s
MAN: "ssdp:discover"
MX: %d
USER-AGENT: %s

`

	searchStr := fmt.Sprintf(template, deviceType, max(timeout/time.Second, 1), userAgent)

	search := []byte(strings.ReplaceAll(searchStr, "\n", "\r\n") + "\r\n")

	logger.Debugf("[upnp] Starting discovery of device type %s on %s", deviceType, intf.Name)

	socket, err := net.ListenMulticastUDP("udp4", intf, &net.UDPAddr{IP: ssdp.IP})
	if err != nil {
		logger.Debugf("[upnp] UPnP discovery: listening to udp multicast: %v", err)
		return
	}
	defer socket.Close() // Make sure our socket gets closed

	logger.Debugf("[upnp] Sending search request for device type %s on %s", deviceType, intf.Name)

	_, err = socket.WriteTo(search, &ssdp)
	if err != nil {
		var e net.Error
		if !errors.As(err, &e) || !e.Timeout() {
			logger.Debugf("[upnp] UPnP discovery: sending search request: %v", err)
		}
		return
	}

	logger.Debugf("[upnp] Listening for UPnP response for device type %s on %s", deviceType, intf.Name)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Listen for responses until a timeout is reached or the context is
	// cancelled
	resp := make([]byte, 65536)
loop:
	for {
		if err := socket.SetDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			logger.Warnf("[upnp] Failed to set UPnP socket deadline: %v", err)
			break
		}

		n, udpAddr, err := socket.ReadFromUDP(resp)
		if err != nil {
			select {
			case <-ctx.Done():
				break loop
			default:
			}
			var ne net.Error
			if ok := errors.As(err, &ne); ok && ne.Timeout() {
				continue // continue reading
			}
			logger.Warnf("[upnp] Failed to read from UPnP socket: %v", err) // legitimate error, not a timeout.
			break
		}

		igds, err := parseResponse(ctx, deviceType, udpAddr, resp[:n], intf)
		if err != nil {
			var unsupp *UnsupportedDeviceTypeError
			if errors.As(err, &unsupp) {
				logger.Debugf("[upnp] %v", err)
			} else if !errors.Is(err, context.Canceled) {
				logger.Warnf("[upnp] Failed to parse UPnP response: %v", err)
			}
			continue
		}
		for _, igd := range igds {
			select {
			case results <- igd:
			case <-ctx.Done():
				return
			}
		}
	}
	logger.Debugf("[upnp] Discovery for device type %s on %s finished.", deviceType, intf.Name)
}

// ssdpResponse is the part of an SSDP search response that we use.
type ssdpResponse struct {
	location string
	uuid     string
}

// parseSSDP reads the headers of an SSDP search response.
func parseSSDP(deviceType string, resp []byte) (ssdpResponse, error) {
	reader := bufio.NewReader(bytes.NewBuffer(resp))
	request := &http.Request{}
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return ssdpResponse{}, err
	}

	respondingDeviceType := response.Header.Get("St")
	if respondingDeviceType != deviceType {
		return ssdpResponse{}, &UnsupportedDeviceTypeError{deviceType: respondingDeviceType}
	}

	deviceDescriptionLocation := response.Header.Get("Location")
	if deviceDescriptionLocation == "" {
		return ssdpResponse{}, errors.New("invalid IGD response: no location specified")
	}

	deviceUSN := response.Header.Get("Usn")
	if deviceUSN == "" {
		return ssdpResponse{}, errors.New("invalid IGD response: USN not specified")
	}

	deviceUUID := strings.TrimPrefix(strings.Split(deviceUSN, "::")[0], "uuid:")
	return ssdpResponse{location: deviceDescriptionLocation, uuid: deviceUUID}, nil
}

func parseResponse(ctx context.Context, deviceType string, _ *net.UDPAddr, resp []byte, netInterface *net.Interface) ([]*Gateway, error) {
	logger.Debugf("[upnp] Handling UPnP response:\n\n%s", resp)

	r, err := parseSSDP(deviceType, resp)
	if err != nil {
		return nil, err
	}

	deviceDescriptionURL, err := url.Parse(r.location)
	if err != nil {
		logger.Warnf("[upnp] Got invalid IGD location: %v", err)
		return nil, err
	}

	// Figure out our IPv4 address on the interface used to reach the IGD.
	localIPv4Address, err := localIPv4(netInterface)
	if err != nil {
		// Try to connect to the IGD and look at which source IP address was used.
		localIPv4Address, err = localIPv4Fallback(ctx, deviceDescriptionURL)
		if err != nil {
			logger.Warnf("[upnp] Unable to determine local IPv4 address for IGD: %v", err)
		}
	}

	return gatewaysAt(ctx, r.uuid, r.location, localIPv4Address, netInterface.Name)
}

// GatewaysAt reads the device description at location and returns its port mapping services,
// using localIP as the internal client of mappings. It skips SSDP: use it when the location
// is already known (and in tests).
func GatewaysAt(ctx context.Context, location string, localIP net.IP) ([]*Gateway, error) {
	return gatewaysAt(ctx, location, location, localIP, "")
}

func gatewaysAt(ctx context.Context, deviceUUID, location string, localIP net.IP, intf string) ([]*Gateway, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode >= 400 {
		return nil, errors.New("bad status code:" + response.Status)
	}

	var upnpRoot upnpRoot
	err = xml.NewDecoder(response.Body).Decode(&upnpRoot)
	if err != nil {
		return nil, err
	}

	return getServiceDescriptions(deviceUUID, localIP, location, upnpRoot.Device, intf)
}

func localIPv4(netInterface *net.Interface) (net.IP, error) {
	addrs, err := netInterface.Addrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}

		if ip.To4() != nil {
			return ip, nil
		}
	}

	return nil, errors.New("no IPv4 address found for interface " + netInterface.Name)
}

func localIPv4Fallback(ctx context.Context, url *url.URL) (net.IP, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(timeoutCtx, "udp4", url.Host)
	if err != nil {
		return nil, err
	}

	defer conn.Close()

	ua, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || ua.IP.To4() == nil {
		return nil, errors.New("tried to obtain IPv4 through fallback but got IPv6 address")
	}
	return ua.IP, nil
}

func getChildDevices(d upnpDevice, deviceType string) []upnpDevice {
	var result []upnpDevice
	for _, dev := range d.Devices {
		if dev.DeviceType == deviceType {
			result = append(result, dev)
		}
	}
	return result
}

func getChildServices(d upnpDevice, serviceType string) []upnpService {
	var result []upnpService
	for _, service := range d.Services {
		if service.Type == serviceType {
			result = append(result, service)
		}
	}
	return result
}

func getServiceDescriptions(deviceUUID string, localIPAddress net.IP, rootURL string, device upnpDevice, netInterface string) ([]*Gateway, error) {
	var result []*Gateway

	switch device.DeviceType {
	case urnIgdV1:
		descriptions := getIGDServices(deviceUUID, localIPAddress, rootURL, device,
			urnWANDeviceV1,
			urnWANConnectionDeviceV1,
			[]string{urnWANIPConnectionV1, urnWANPPPConnectionV1},
			netInterface)

		result = append(result, descriptions...)
	case urnIgdV2:
		descriptions := getIGDServices(deviceUUID, localIPAddress, rootURL, device,
			urnWANDeviceV2,
			urnWANConnectionDeviceV2,
			[]string{urnWANIPConnectionV2, urnWANPPPConnectionV2},
			netInterface)

		result = append(result, descriptions...)
	default:
		return result, errors.New("[" + rootURL + "] Malformed root device description: not an InternetGatewayDevice.")
	}

	if len(result) < 1 {
		return result, errors.New("[" + rootURL + "] Malformed device description: no compatible service descriptions found.")
	}
	return result, nil
}

func getIGDServices(deviceUUID string, localIPAddress net.IP, rootURL string, device upnpDevice, wanDeviceURN string, wanConnectionURN string, URNs []string, netInterface string) []*Gateway {
	var result []*Gateway

	devices := getChildDevices(device, wanDeviceURN)

	if len(devices) < 1 {
		logger.Warnf("[upnp] Got malformed InternetGatewayDevice description: no WANDevices specified")
		return result
	}

	for _, wanDevice := range devices {
		connections := getChildDevices(wanDevice, wanConnectionURN)

		if len(connections) < 1 {
			logger.Warnf("[upnp] Got malformed WAN device description: no WANConnectionDevices specified (%s)", wanDeviceURN)
		}

		for _, connection := range connections {
			for _, urn := range URNs {
				services := getChildServices(connection, urn)

				if len(services) == 0 {
					logger.Debugf("[upnp] %s - no services of type %s found on connection.", rootURL, urn)
				}

				for _, service := range services {
					if service.ControlURL == "" {
						logger.Warnf("[upnp] Got malformed service description: no control URL (%s)", service.Type)
					} else {
						u, _ := url.Parse(rootURL)
						replaceRawPath(u, service.ControlURL)

						logger.Debugf("[upnp] %s - found %s with URL %s", rootURL, service.Type, u)

						result = append(result, &Gateway{
							UUID:         deviceUUID,
							FriendlyName: device.FriendlyName,
							ServiceID:    service.ID,
							URL:          u.String(),
							URN:          service.Type,
							Interface:    netInterface,
							LocalIPv4:    localIPAddress,
						})
					}
				}
			}
		}
	}

	return result
}

func replaceRawPath(u *url.URL, rp string) {
	asURL, err := url.Parse(rp)
	if err != nil {
		return
	} else if asURL.IsAbs() {
		u.Path = asURL.Path
		u.RawQuery = asURL.RawQuery
	} else {
		var p, q string
		fs := strings.Split(rp, "?")
		p = fs[0]
		if len(fs) > 1 {
			q = fs[1]
		}

		if p != "" && p[0] == '/' {
			u.Path = p
		} else {
			u.Path += p
		}
		u.RawQuery = q
	}
}

func soapRequest(ctx context.Context, url, service, function, message string) ([]byte, error) {
	return soapRequestWithIP(ctx, url, service, function, message, nil)
}

func soapRequestWithIP(ctx context.Context, url, service, function, message string, localIP *net.TCPAddr) ([]byte, error) {
	const template = `<?xml version="1.0" ?>
	<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
	<s:Body>%s</s:Body>
	</s:Envelope>
`
	var resp []byte

	body := fmt.Sprintf(template, message)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return resp, err
	}
	req.Close = true
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("User-Agent", userAgent)
	req.Header["SOAPAction"] = []string{fmt.Sprintf(`"%s#%s"`, service, function)} // Enforce capitalization in header-entry for sensitive routers. See syncthing issue #1696
	req.Header.Set("Connection", "Close")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")

	logger.Debugf("[upnp] SOAP Request URL: %s, action %s", url, req.Header.Get("SOAPAction"))

	dialer := net.Dialer{
		LocalAddr: localIP,
	}
	transport := &http.Transport{
		DialContext: dialer.DialContext,
	}
	httpClient := &http.Client{
		Transport: transport,
	}
	r, err := httpClient.Do(req)
	if err != nil {
		logger.Debugf("[upnp] SOAP do: %v", err)
		return resp, err
	}
	defer r.Body.Close()

	resp, err = io.ReadAll(r.Body)
	if err != nil {
		logger.Debugf("[upnp] Error reading SOAP response: %v, partial response (if present):\n\n%s", err, resp)
		return resp, err
	}

	logger.Debugf("[upnp] SOAP Response: %s\n\n%s\n\n", r.Status, resp)

	if r.StatusCode >= 400 {
		return resp, soapError(function, r.Status, resp)
	}

	return resp, nil
}

type soapGetExternalIPAddressResponseEnvelope struct {
	XMLName xml.Name
	Body    soapGetExternalIPAddressResponseBody `xml:"Body"`
}

type soapGetExternalIPAddressResponseBody struct {
	XMLName                      xml.Name
	GetExternalIPAddressResponse getExternalIPAddressResponse `xml:"GetExternalIPAddressResponse"`
}

type getExternalIPAddressResponse struct {
	NewExternalIPAddress string `xml:"NewExternalIPAddress"`
}

type soapErrorResponse struct {
	ErrorCode        int    `xml:"Body>Fault>detail>UPnPError>errorCode"`
	ErrorDescription string `xml:"Body>Fault>detail>UPnPError>errorDescription"`
}

// SOAPError is a UPnP error returned by the gateway (a SOAP fault with a UPnPError detail).
// Common codes: 718 ConflictInMappingEntry, 725 OnlyPermanentLeasesSupported, 606 Action not authorized.
type SOAPError struct {
	Action      string
	Code        int
	Description string
}

func (e *SOAPError) Error() string {
	return fmt.Sprintf("UPnP %s: %s (%d)", e.Action, e.Description, e.Code)
}

// soapError turns an HTTP error response into *SOAPError when it carries a UPnPError,
// otherwise into a plain error with the HTTP status.
func soapError(function, status string, resp []byte) error {
	var envelope soapErrorResponse
	if err := xml.Unmarshal(resp, &envelope); err == nil && envelope.ErrorCode != 0 {
		return &SOAPError{Action: function, Code: envelope.ErrorCode, Description: envelope.ErrorDescription}
	}
	return errors.New(function + ": " + status)
}
