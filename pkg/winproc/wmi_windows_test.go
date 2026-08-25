//go:build windows

package winproc

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
