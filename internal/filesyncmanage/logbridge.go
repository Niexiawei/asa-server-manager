package filesyncmanage

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"asa-server/pkg/logger"
)

// logPrefix 让前端的日志面板能按前缀筛出同步库的记录（同 frpmanage 的 [frpc]）。
const logPrefix = "[filesync] "

// logHandler 把同步库的 log/slog 记录转进本项目的 pkg/logger。
//
// 经 client.Config.Logger 按节点注入，不调用同步库的 logger.SetDefault——那是进程级的，
// 会把同步库里任何其他地方的日志也一并接管。
type logHandler struct {
	attrs  []slog.Attr
	groups []string
}

func newLogger() *slog.Logger { return slog.New(&logHandler{}) }

// Enabled 全部放行：级别过滤由 pkg/logger 按自己的配置做，这里再过滤一次只会
// 让两处阈值不一致。
func (h *logHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *logHandler) Handle(_ context.Context, record slog.Record) error {
	var b strings.Builder
	b.WriteString(logPrefix)
	b.WriteString(record.Message)
	for _, attr := range h.attrs {
		writeAttr(&b, nil, attr) // 已在 WithAttrs 时带上了当时的分组前缀
	}
	record.Attrs(func(attr slog.Attr) bool {
		writeAttr(&b, h.groups, attr)
		return true
	})
	line := b.String()
	switch {
	case record.Level >= slog.LevelError:
		logger.Error(line)
	case record.Level >= slog.LevelWarn:
		logger.Warn(line)
	case record.Level >= slog.LevelInfo:
		logger.Info(line)
	default:
		logger.Debug(line)
	}
	return nil
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	qualified := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	qualified = append(qualified, h.attrs...)
	for _, attr := range attrs {
		// 属性在加入时就带上当时的分组前缀，之后再 WithGroup 不影响它们。
		attr.Key = strings.Join(append(append([]string(nil), h.groups...), attr.Key), ".")
		qualified = append(qualified, attr)
	}
	return &logHandler{attrs: qualified, groups: h.groups}
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &logHandler{attrs: h.attrs, groups: append(append([]string(nil), h.groups...), name)}
}

// writeAttr 以 " group.key=value" 追加一个属性，嵌套分组逐层展开。
func writeAttr(b *strings.Builder, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		nested := append(append([]string(nil), groups...), attr.Key)
		for _, inner := range attr.Value.Group() {
			writeAttr(b, nested, inner)
		}
		return
	}
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}
	fmt.Fprintf(b, " %s=%v", key, attr.Value.Any())
}
