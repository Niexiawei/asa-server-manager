package installer

import "testing"

// ArkApi 主程序操作与 Steam 更新共用一个「更新中」标记：任一方进行中，另一方都必须被拒。
// 否则先结束的一方会把标记清掉，另一方就在没有标记的状态下改写 server-files，
// 启动侧的 IsUpdatingServerFiles 检查随之失效。
func TestArkApiWriteExcludesServerFilesUpdate(t *testing.T) {
	end, err := BeginArkApiWrite()
	if err != nil {
		t.Fatalf("BeginArkApiWrite() 意外失败: %v", err)
	}
	if !IsUpdatingServerFiles() {
		t.Error("主程序操作期间应报告「更新中」，实例启动据此拒绝")
	}
	if _, err := BeginArkApiWrite(); err == nil {
		t.Error("两个主程序操作不能同时进行")
	}
	if err := beginServerFilesUpdate(); err == nil {
		t.Error("主程序操作进行中不能开始 Steam 更新")
	}
	end()
	if IsUpdatingServerFiles() {
		t.Fatal("end 之后标记应被清除，否则实例将永远无法启动")
	}

	if err := beginServerFilesUpdate(); err != nil {
		t.Fatalf("beginServerFilesUpdate() 意外失败: %v", err)
	}
	if _, err := BeginArkApiWrite(); err == nil {
		t.Error("Steam 更新进行中不能做主程序操作")
	}
	endServerFilesUpdate()
}
