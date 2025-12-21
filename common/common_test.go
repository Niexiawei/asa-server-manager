package common

import (
	"fmt"
	"testing"
)

func Test_QueryProcess(t *testing.T) {
	res, err := QueryProcess("ArkAscendedServer.exe", "Port=9310")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(res)
}

func TestResolveDomainToIPv4(t *testing.T) {
	ips, err := ResolveDomainToIPv4("asa.nicoi.cn")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(ips)
}
