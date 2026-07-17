package tail

import (
	"fmt"
	"testing"
)

func Test_tail(t *testing.T) {
	tailer, logChan, err := NewTailer("E:\\asa_server_data\\logs\\asaServer.log", 500)
	if err != nil {
		t.Error(err)
	}
	tailer.Start()

	for {
		select {
		case log := <-logChan:
			fmt.Println(log)
		}
	}
}
