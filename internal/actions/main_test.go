package actions

import (
	"os"
	"testing"

	"asa-server/internal/appconfig"
)

// config / setup 的用例都会走 appconfig.Load：开发机上的 ASA_* 环境变量会改写
// 它们读到的配置，先全部清掉（docs/TEST_ENV_COUPLING_PLAN.md T1）。
func TestMain(m *testing.M) {
	appconfig.UnsetEnvForTest()
	os.Exit(m.Run())
}
