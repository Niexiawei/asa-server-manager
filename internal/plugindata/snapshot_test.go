package plugindata

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// 快照必须走 SQLite 自己的在线备份，而不是定时文件复制。
//
// 这条用一个**真库**来验：WAL 模式下数据大部分还压在 -wal 里没 checkpoint，
// 朴素的逐文件拷会拷出主库与 -wal 互不一致的组合，得到损坏的快照；
// VACUUM INTO 产出的是一致的、已 checkpoint 的单文件副本。
func TestSnapshotDBProducesConsistentCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ArkDB.db")

	db, err := sql.Open("sqlite", filepath.ToSlash(src)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("建库: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE perms (id INTEGER PRIMARY KEY, player TEXT, grp TEXT)`); err != nil {
		t.Fatalf("建表: %v", err)
	}
	for i := range 50 {
		if _, err := db.Exec(`INSERT INTO perms (player, grp) VALUES (?, ?)`, i, "Admins"); err != nil {
			t.Fatalf("写数据: %v", err)
		}
	}
	// 刻意不 checkpoint、不关闭连接：模拟服务器正在运行时做快照
	defer db.Close()

	if _, err := os.Stat(src + "-wal"); err != nil {
		t.Logf("提示：本次没有产生 -wal（%v），用例仍然有效但覆盖面变窄", err)
	}

	dst := filepath.Join(dir, snapshotsDirName, "ArkDB.db")
	if err := snapshotDB(src, dst); err != nil {
		t.Fatalf("snapshotDB: %v", err)
	}

	// 快照必须是自包含的单文件：不该带出需要一并搬运的 -wal
	if _, err := os.Stat(dst + "-wal"); !os.IsNotExist(err) {
		t.Error("VACUUM INTO 的产物应是已 checkpoint 的单文件，不该有 -wal")
	}

	snap, err := sql.Open("sqlite", filepath.ToSlash(dst))
	if err != nil {
		t.Fatalf("打开快照: %v", err)
	}
	defer snap.Close()

	var n int
	if err := snap.QueryRow(`SELECT count(*) FROM perms`).Scan(&n); err != nil {
		t.Fatalf("查询快照失败（快照损坏或不完整）: %v", err)
	}
	if n != 50 {
		t.Errorf("快照里只有 %d 行，期望 50 —— 未 checkpoint 的 WAL 数据没被带上", n)
	}
}

// 保留一代旧快照：新快照写坏时还有一份能用的。
func TestSnapshotDBRotatesPreviousGeneration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ArkDB.db")
	dst := filepath.Join(dir, snapshotsDirName, "ArkDB.db")

	makeDB := func(table string) {
		db, err := sql.Open("sqlite", filepath.ToSlash(src))
		if err != nil {
			t.Fatalf("建库: %v", err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS ` + table + ` (x INTEGER)`); err != nil {
			t.Fatalf("建表: %v", err)
		}
	}

	makeDB("gen1")
	if err := snapshotDB(src, dst); err != nil {
		t.Fatalf("第一次快照: %v", err)
	}
	makeDB("gen2")
	if err := snapshotDB(src, dst); err != nil {
		t.Fatalf("第二次快照: %v", err)
	}

	if _, err := os.Stat(dst + ".1"); err != nil {
		t.Fatalf("应保留上一代快照: %v", err)
	}

	prev, err := sql.Open("sqlite", filepath.ToSlash(dst+".1"))
	if err != nil {
		t.Fatalf("打开旧快照: %v", err)
	}
	defer prev.Close()

	var name string
	err = prev.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='gen2'`).Scan(&name)
	if err == nil {
		t.Error("dst.1 应是上一代快照，不该含有第二次快照才有的表")
	}
}
