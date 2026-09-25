package filesyncmanage

import (
	"log/slog"
	"strings"
	"testing"
)

func TestWriteAttrQualifiesGroups(t *testing.T) {
	var b strings.Builder
	writeAttr(&b, nil, slog.String("path", "a.txt"))
	writeAttr(&b, []string{"task"}, slog.Int("attempt", 2))
	writeAttr(&b, nil, slog.Group("root", slog.String("group_id", "cluster-x"), slog.Group("stats", slog.Int("n", 1))))
	writeAttr(&b, nil, slog.Attr{}) // 空属性按 slog 约定忽略
	want := " path=a.txt task.attempt=2 root.group_id=cluster-x root.stats.n=1"
	if b.String() != want {
		t.Fatalf("got %q\nwant %q", b.String(), want)
	}
}

// TestWithAttrsKeepsTheGroupsInEffectWhenAdded：WithAttrs 加入的属性带的是加入时的分组，
// 之后的 WithGroup 只作用于后来的属性——这是 slog.Handler 的约定。
func TestWithAttrsKeepsTheGroupsInEffectWhenAdded(t *testing.T) {
	h := (&logHandler{}).WithGroup("node").WithAttrs([]slog.Attr{slog.String("id", "n1")}).WithGroup("root").(*logHandler)
	if len(h.attrs) != 1 || h.attrs[0].Key != "node.id" {
		t.Fatalf("attrs = %+v, want node.id", h.attrs)
	}
	if strings.Join(h.groups, ".") != "node.root" {
		t.Fatalf("groups = %v", h.groups)
	}
}
