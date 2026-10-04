package appconfig

import (
	"os"
	"testing"
)

// 开发机上的 ASA_* 环境变量会经 viper 的 AutomaticEnv 改写测试读到的配置，
// 先全部清掉（docs/TEST_ENV_COUPLING_PLAN.md T1）。
func TestMain(m *testing.M) {
	UnsetEnvForTest()
	os.Exit(m.Run())
}
