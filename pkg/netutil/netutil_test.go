package netutil

import (
	"fmt"
	"testing"
)

func TestResolveDomainToIPv4(t *testing.T) {
	ips, err := ResolveDomainToIPv4("asa.nicoi.cn")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(ips)
}
