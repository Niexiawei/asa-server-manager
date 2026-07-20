package installer

import "testing"

// 标记卡住会让所有实例永远起不来，是本机制最坏的失败模式，
// 因此单独锁住 begin/end 的配对行为。
func TestServerFilesUpdateFlagLifecycle(t *testing.T) {
	if IsUpdatingServerFiles() {
		t.Fatal("初始状态应为「未更新」")
	}

	if err := beginServerFilesUpdate(); err != nil {
		// 测试环境下 InstancesDir 通常为空，不应有存活实例
		t.Fatalf("beginServerFilesUpdate() 意外失败: %v", err)
	}

	if !IsUpdatingServerFiles() {
		t.Error("begin 之后应报告「更新中」")
	}

	endServerFilesUpdate()

	if IsUpdatingServerFiles() {
		t.Error("end 之后标记应被清除，否则实例将永远无法启动")
	}
}

// 更新函数在中途 return error 时，defer 必须已经把标记清掉。
// 这里直接模拟「begin 成功后函数体失败」的路径。
func TestServerFilesUpdateFlagClearedOnFailure(t *testing.T) {
	failingUpdate := func() error {
		if err := beginServerFilesUpdate(); err != nil {
			return err
		}
		defer endServerFilesUpdate()

		return errFake
	}

	if err := failingUpdate(); err != errFake {
		t.Fatalf("期望返回 errFake，实际 %v", err)
	}

	if IsUpdatingServerFiles() {
		t.Error("更新失败后标记仍未清除，实例将无法启动")
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "fake failure" }

var errFake = fakeErr{}
