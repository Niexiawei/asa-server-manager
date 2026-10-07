// Copyright (C) 2016 The Syncthing Authors.
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

// Modified for asa-server (2026-10): taken from Syncthing lib/upnp/igd_service.go
// (commit 2ca95cf1). Changes: IGDService renamed Gateway and no longer implements
// Syncthing's nat.Device; the IPv6 pinhole methods were removed; AddPortMapping
// reports the lease actually granted (after the 725 fallback) and returns
// *SOAPError; Device kept only as FriendlyName.

package upnp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Protocol is the transport protocol of a port mapping.
type Protocol string

const (
	TCP Protocol = "TCP"
	UDP Protocol = "UDP"
)

// errCodeOnlyPermanentLeases is UPnP error 725 OnlyPermanentLeasesSupported.
const errCodeOnlyPermanentLeases = 725

// ErrCodeConflict is UPnP error 718 ConflictInMappingEntry: the external port is mapped to another client.
const ErrCodeConflict = 718

// A Gateway is a port mapping service (WANIPConnection / WANPPPConnection) provided by an IGD.
type Gateway struct {
	UUID         string
	FriendlyName string
	ServiceID    string
	URL          string
	URN          string
	LocalIPv4    net.IP
	Interface    string
}

// AddPortMapping adds a port mapping to the specified IGD service. It returns the lease actually
// granted: routers that only support permanent mappings (error 725) are retried with a lease of 0.
// Adding the same external port again for the same internal client renews the mapping.
func (s *Gateway) AddPortMapping(ctx context.Context, protocol Protocol, internalPort, externalPort int, description string, duration time.Duration) (time.Duration, error) {
	if s.LocalIPv4 == nil {
		return 0, errors.New("no local IPv4")
	}

	const template = `<u:AddPortMapping xmlns:u="%s">
		<NewRemoteHost></NewRemoteHost>
		<NewExternalPort>%d</NewExternalPort>
		<NewProtocol>%s</NewProtocol>
		<NewInternalPort>%d</NewInternalPort>
		<NewInternalClient>%s</NewInternalClient>
		<NewEnabled>1</NewEnabled>
		<NewPortMappingDescription>%s</NewPortMappingDescription>
		<NewLeaseDuration>%d</NewLeaseDuration>
		</u:AddPortMapping>`
	body := fmt.Sprintf(template, s.URN, externalPort, protocol, internalPort, s.LocalIPv4, xmlEscape(description), duration/time.Second)

	_, err := soapRequestWithIP(ctx, s.URL, s.URN, "AddPortMapping", body, &net.TCPAddr{IP: s.LocalIPv4})
	if err != nil && duration > 0 {
		// Try to repair error code 725 - OnlyPermanentLeasesSupported
		var se *SOAPError
		if errors.As(err, &se) && se.Code == errCodeOnlyPermanentLeases {
			return s.AddPortMapping(ctx, protocol, internalPort, externalPort, description, 0)
		}
	}
	if err != nil {
		return 0, err
	}
	return duration, nil
}

// DeletePortMapping deletes a port mapping from the specified IGD service.
func (s *Gateway) DeletePortMapping(ctx context.Context, protocol Protocol, externalPort int) error {
	const template = `<u:DeletePortMapping xmlns:u="%s">
	<NewRemoteHost></NewRemoteHost>
	<NewExternalPort>%d</NewExternalPort>
	<NewProtocol>%s</NewProtocol>
	</u:DeletePortMapping>`

	body := fmt.Sprintf(template, s.URN, externalPort, protocol)

	_, err := soapRequest(ctx, s.URL, s.URN, "DeletePortMapping", body)
	return err
}

// GetExternalIPv4Address queries the IGD service for its external IP address.
// Returns nil if the external IP address is invalid or undefined, along with
// any relevant errors
func (s *Gateway) GetExternalIPv4Address(ctx context.Context) (net.IP, error) {
	const template = `<u:GetExternalIPAddress xmlns:u="%s" />`

	body := fmt.Sprintf(template, s.URN)
	response, err := soapRequest(ctx, s.URL, s.URN, "GetExternalIPAddress", body)
	if err != nil {
		return nil, err
	}

	var envelope soapGetExternalIPAddressResponseEnvelope
	if err := xml.Unmarshal(response, &envelope); err != nil {
		return nil, err
	}

	result := net.ParseIP(envelope.Body.GetExternalIPAddressResponse.NewExternalIPAddress)

	return result, nil
}

// ID returns a unique ID for the service
func (s *Gateway) ID() string {
	return s.UUID + "/" + s.FriendlyName + "/" + s.ServiceID + "/" + s.URN + "/" + s.URL
}

// IsV2 reports whether the service is from an IGDv2 device.
func (s *Gateway) IsV2() bool {
	return s.URN == urnWANIPConnectionV2 || s.URN == urnWANPPPConnectionV2
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
