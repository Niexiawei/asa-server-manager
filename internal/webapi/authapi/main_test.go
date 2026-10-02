package authapi

import (
	"os"
	"testing"

	"asa-server/internal/appconfig"
)

// setupEnv 走 appconfig.Load：开发机上的 ASA_AUTH_* 之类会直接改写鉴权配置，
// 先全部清掉（docs/TEST_ENV_COUPLING_PLAN.md T1）。
func TestMain(m *testing.M) {
	appconfig.UnsetEnvForTest()
	os.Exit(m.Run())
}
