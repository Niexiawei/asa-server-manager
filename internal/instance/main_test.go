package instance

import (
	"os"
	"testing"

	"asa-server/pkg/logger"
)

// 日志写进本次测试专用的临时目录，跑完删掉（docs/TEST_ENV_COUPLING_PLAN.md T6）。
func TestMain(m *testing.M) {
	cleanup := logger.InitTempForTest()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
