//go:build windows

package winproc

import (
	"fmt"
	"testing"
)

func Test_GetPIDByPort(t *testing.T) {
	pid, err := GetPIDByPort(9234)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(pid)
}
