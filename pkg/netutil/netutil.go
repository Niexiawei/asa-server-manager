// Package netutil provides DNS resolution helpers with no domain dependencies.
package netutil

import (
	"fmt"
	"net"
)

// ResolveDomainToIP resolves a domain name to its IP addresses.
func ResolveDomainToIP(domain string) ([]string, error) {
	ips, err := net.LookupIP(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve domain %s: %w", domain, err)
	}

	var ipStrings []string
	for _, ip := range ips {
		ipStrings = append(ipStrings, ip.String())
	}

	return ipStrings, nil
}

// ResolveDomainToIPv4 resolves a domain name to its IPv4 addresses only.
func ResolveDomainToIPv4(domain string) ([]string, error) {
	ips, err := net.LookupIP(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve domain %s: %w", domain, err)
	}

	var ipStrings []string
	for _, ip := range ips {
		// Check if it's an IPv4 address
		if ip.To4() != nil {
			ipStrings = append(ipStrings, ip.String())
		}
	}

	return ipStrings, nil
}

// ResolveDomainToIPv6 resolves a domain name to its IPv6 addresses only.
func ResolveDomainToIPv6(domain string) ([]string, error) {
	ips, err := net.LookupIP(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve domain %s: %w", domain, err)
	}

	var ipStrings []string
	for _, ip := range ips {
		// Check if it's an IPv6 address (and not IPv4)
		if ip.To4() == nil && ip.To16() != nil {
			ipStrings = append(ipStrings, ip.String())
		}
	}

	return ipStrings, nil
}
