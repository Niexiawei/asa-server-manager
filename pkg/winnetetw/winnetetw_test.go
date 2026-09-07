//go:build windows

package winnetetw

import (
	"encoding/binary"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// ---- 结构体布局钉子测试 ----
//
// ETW/TDH 的 API 用这些结构体按字段偏移读写。Go 与 C 的排布规则一致，
// 但字段顺序、显式填充、union 折叠任何一处手滑都会静默错位——
// StartTraceW 可能照常成功，然后 ETW 拿野指针当回调直接崩进程。
// 这里把 2026-09 逐页核对 Microsoft Learn 得到的 x64 布局全部钉死。

func TestStructSizes(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"wnodeHeader", unsafe.Sizeof(wnodeHeader{}), 48},
		{"eventTraceProperties", unsafe.Sizeof(eventTraceProperties{}), 120},
		{"eventTraceHeader", unsafe.Sizeof(eventTraceHeader{}), 48},
		{"eventTrace", unsafe.Sizeof(eventTrace{}), 88},
		{"traceLogfileHeader", unsafe.Sizeof(traceLogfileHeader{}), 280},
		{"eventTraceLogfileW", unsafe.Sizeof(eventTraceLogfileW{}), 448},
		{"eventDescriptor", unsafe.Sizeof(eventDescriptor{}), 16},
		{"eventHeader", unsafe.Sizeof(eventHeader{}), 80},
		{"etwBufferContext", unsafe.Sizeof(etwBufferContext{}), 2},
		{"eventRecord", unsafe.Sizeof(eventRecord{}), 112},
		{"enableTraceParameters", unsafe.Sizeof(enableTraceParameters{}), 48},
		{"eventFilterDescriptor", unsafe.Sizeof(eventFilterDescriptor{}), 16},
		{"propertyDataDescriptor", unsafe.Sizeof(propertyDataDescriptor{}), 16},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s 大小 %d，应为 %d", c.name, c.got, c.want)
		}
	}
}

func TestFieldOffsets(t *testing.T) {
	type off struct {
		name string
		got  uintptr
		want uintptr
	}
	var (
		props  eventTraceProperties
		logf   eventTraceLogfileW
		rec    eventRecord
		eh     eventHeader
		params enableTraceParameters
	)
	cases := []off{
		// eventTraceProperties：Wnode(48) + 7×ULONG + AgeLimit union(76)
		// + 6×out ULONG(80..103) + LoggerThreadId(104) + 2×Offset(112,116)
		{"props.Wnode", unsafe.Offsetof(props.Wnode), 0},
		{"props.LogFileMode", unsafe.Offsetof(props.LogFileMode), 64},
		{"props.EventsLost", unsafe.Offsetof(props.EventsLost), 88},
		{"props.RealTimeBuffersLost", unsafe.Offsetof(props.RealTimeBuffersLost), 100},
		{"props.LoggerThreadId", unsafe.Offsetof(props.LoggerThreadId), 104},
		{"props.LogFileNameOffset", unsafe.Offsetof(props.LogFileNameOffset), 112},
		{"props.LoggerNameOffset", unsafe.Offsetof(props.LoggerNameOffset), 116},

		// eventTraceLogfileW：ProcessTraceMode 在 offset 28 的 union 里，
		// EventRecordCallback 在 424，Context 在 440——OpenTraceW 按 C 布局读这些位置
		{"logf.ProcessTraceMode", unsafe.Offsetof(logf.ProcessTraceMode), 28},
		{"logf.CurrentEvent", unsafe.Offsetof(logf.CurrentEvent), 32},
		{"logf.LogfileHeader", unsafe.Offsetof(logf.LogfileHeader), 120},
		{"logf.EventRecordCallback", unsafe.Offsetof(logf.EventRecordCallback), 424},
		{"logf.IsKernelTrace", unsafe.Offsetof(logf.IsKernelTrace), 432},
		{"logf.Context", unsafe.Offsetof(logf.Context), 440},

		// eventRecord：callback 入参，UserContext 在 104
		{"rec.EventHeader", unsafe.Offsetof(rec.EventHeader), 0},
		{"rec.BufferContext", unsafe.Offsetof(rec.BufferContext), 80},
		{"rec.UserDataLength", unsafe.Offsetof(rec.UserDataLength), 84},
		{"rec.ExtendedData", unsafe.Offsetof(rec.ExtendedData), 88},
		{"rec.UserData", unsafe.Offsetof(rec.UserData), 96},
		{"rec.UserContext", unsafe.Offsetof(rec.UserContext), 104},

		// eventHeader：ProcessId 在 12（注意不可靠，PID 从 payload 解析，见 etw_parse.go）
		{"eh.ProcessId", unsafe.Offsetof(eh.ProcessId), 12},
		{"eh.TimeStamp", unsafe.Offsetof(eh.TimeStamp), 16},
		{"eh.EventDescriptor", unsafe.Offsetof(eh.EventDescriptor), 40},
		{"eh.ActivityId", unsafe.Offsetof(eh.ActivityId), 64},

		// enableTraceParameters：SourceId 只需 4 对齐（C 侧 offset 12），Go 自动满足
		{"params.SourceId", unsafe.Offsetof(params.SourceId), 12},
		{"params.EnableFilterDesc", unsafe.Offsetof(params.EnableFilterDesc), 32},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s 偏移 %d，应为 %d", c.name, c.got, c.want)
		}
	}
}

// ---- 事件映射 ----

func TestClassifyEvent(t *testing.T) {
	if len(kernelNetworkEvents) != 8 {
		t.Fatalf("事件表应有 8 个 ID，实际 %d", len(kernelNetworkEvents))
	}
	// TCP/UDP × RX/TX 四象限每个都要有两个（v4/v6）
	counts := make(map[netKind]int)
	for _, k := range kernelNetworkEvents {
		counts[k]++
	}
	for k, n := range counts {
		if n != 2 {
			t.Errorf("netKind %d 应有 2 个事件（v4/v6），实际 %d", k, n)
		}
	}
	if _, ok := classifyEvent(1); ok {
		t.Error("Event ID 1 不应被识别")
	}
	if k, ok := classifyEvent(43); !ok || k != kindUDP_RX {
		t.Errorf("Event ID 43 应为 UDP_RX，得到 %v %v", k, ok)
	}
}

// ---- properties / 过滤器拼装 ----

func TestBuildPropertiesBuffer(t *testing.T) {
	name := utf16FromString(sessionName)
	buf := buildPropertiesBuffer(name)
	props := (*eventTraceProperties)(unsafe.Pointer(&buf[0]))

	if got := unsafe.Sizeof(eventTraceProperties{}); uintptr(len(buf)) <= got {
		t.Fatalf("缓冲区 %d 字节必须大于结构体 %d", len(buf), got)
	}
	if props.Wnode.BufferSize != uint32(len(buf)) {
		t.Errorf("Wnode.BufferSize = %d，应为整个缓冲 %d", props.Wnode.BufferSize, len(buf))
	}
	if props.Wnode.Flags != wnodeFlagTracedGuid {
		t.Errorf("Wnode.Flags = %#x", props.Wnode.Flags)
	}
	if props.LogFileMode != eventTraceRealTimeMode {
		t.Errorf("LogFileMode = %#x，实时会话应为 %#x", props.LogFileMode, eventTraceRealTimeMode)
	}
	// LoggerName 偏移处应是 UTF-16 的 session 名
	nameOff := int(props.LoggerNameOffset)
	got := make([]uint16, 0, len(name))
	for p := nameOff; p+2 <= len(buf); p += 2 {
		u := binary.LittleEndian.Uint16(buf[p : p+2])
		if u == 0 {
			break
		}
		got = append(got, u)
	}
	if string(utf16Decode(got)) != sessionName {
		t.Errorf("LoggerName 区读出 %q，应为 %q", utf16Decode(got), sessionName)
	}
	// LogFileName 偏移处应是空串（首字节为零）
	if buf[props.LogFileNameOffset] != 0 || buf[props.LogFileNameOffset+1] != 0 {
		t.Error("LogFileName 区应为空 UTF-16 串")
	}
}

func utf16Decode(u []uint16) []rune {
	out := make([]rune, len(u))
	for i, v := range u {
		out[i] = rune(v)
	}
	return out
}

func TestBuildEventIDFilter(t *testing.T) {
	buf := buildEventIDFilter()
	wantLen := 4 + len(kernelNetworkEvents)*2
	if len(buf) != wantLen {
		t.Fatalf("过滤器 %d 字节，应为 %d", len(buf), wantLen)
	}
	if buf[0] != 1 {
		t.Error("FilterIn 应为 TRUE")
	}
	if got := binary.LittleEndian.Uint16(buf[2:4]); got != uint16(len(kernelNetworkEvents)) {
		t.Errorf("Count = %d，应为 %d", got, len(kernelNetworkEvents))
	}
	// 每个 ID 恰好出现一次
	seen := make(map[uint16]bool)
	for i := 4; i+2 <= len(buf); i += 2 {
		seen[binary.LittleEndian.Uint16(buf[i:i+2])] = true
	}
	if len(seen) != len(kernelNetworkEvents) {
		t.Errorf("过滤器含 %d 个不同 ID，应为 %d", len(seen), len(kernelNetworkEvents))
	}
	for id := range kernelNetworkEvents {
		if !seen[id] {
			t.Errorf("Event ID %d 未包含在过滤器里", id)
		}
	}
}

// ---- payload 读取 ----

func TestReadUintLE(t *testing.T) {
	b := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	if v, ok := readUintLE(b, 0, 1); !ok || v != 1 {
		t.Errorf("1 字节读出 %d %v", v, ok)
	}
	if v, ok := readUintLE(b, 0, 2); !ok || v != 0x0201 {
		t.Errorf("2 字节读出 %#x %v", v, ok)
	}
	if v, ok := readUintLE(b, 1, 4); !ok || v != 0x05040302 {
		t.Errorf("4 字节读出 %#x %v", v, ok)
	}
	if v, ok := readUintLE(b, 1, 8); !ok || v != 0x05040302 { // 截断到 uint32
		t.Errorf("8 字节读出 %#x %v", v, ok)
	}
	// 越界必须安全返回 false（callback 里 panic 会带崩进程）
	if _, ok := readUintLE(b, 8, 4); ok {
		t.Error("越界读取应返回 false")
	}
	if _, ok := readUintLE(b, 0, 3); ok {
		t.Error("非法宽度 3 应返回 false")
	}
}

// ---- schema 布局推算 ----
//
// 构造一个最小的合成 TRACE_EVENT_INFO：头部 + 2 个 EVENT_PROPERTY_INFO
// （pid:UInt32 + size:UInt32）+ 属性名区，验证 analyzeSchema 的快路径推算。

func TestAnalyzeSchemaFastPath(t *testing.T) {
	const propCount = 2
	names := "pid\x00size\x00" // 属性名区从 112 + 2*24 = 160 开始
	nameBase := 112 + propCount*24
	total := nameBase + len(names)*2 // names 先按字节算再乘 2（ASCII）
	buf := make([]byte, total)

	putU32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(buf[off:], v) }
	putU16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(buf[off:], v) }

	putU32(100, propCount) // PropertyCount（头部 offset 100）
	putU32(104, propCount) // TopLevelPropertyCount（头部 offset 104）

	// 属性 0：pid，UInt32
	p0 := 112
	putU32(p0, 0)                  // Flags
	putU32(p0+4, uint32(nameBase)) // NameOffset → "pid"
	putU16(p0+8, tdhInTypeUInt32)  // InType
	putU16(p0+16, 1)               // count（offset 16，非数组恒 1）
	// 属性 1：size，UInt32
	p1 := p0 + 24
	putU32(p1, 0)
	putU32(p1+4, uint32(nameBase+8)) // "size"（pid\0 = 8 字节 UTF-16）
	putU16(p1+8, tdhInTypeUInt32)
	putU16(p1+16, 1)

	// 属性名区
	for i := 0; i < len(names); i++ {
		putU16(nameBase+i*2, uint16(names[i]))
	}

	s := analyzeSchema(buf)
	if s.state != schemaFast {
		t.Fatalf("应为 schemaFast，得到 state=%d", s.state)
	}
	if s.pidOffset != 0 || s.sizeOffset != 4 {
		t.Errorf("offset 推算错误：pid=%d size=%d，应为 0 和 4", s.pidOffset, s.sizeOffset)
	}
	if s.pidSize != 4 || s.sizeSize != 4 {
		t.Errorf("宽度错误：pid=%d size=%d", s.pidSize, s.sizeSize)
	}
	if string(utf16Decode(toUint16(s.pidNameUTF16))) != "pid\x00" {
		t.Errorf("pidNameUTF16 = %q", s.pidNameUTF16)
	}
}

func TestAnalyzeSchemaUnknownNames(t *testing.T) {
	// 属性名完全对不上 → schemaFailed（绝不猜）
	buf := make([]byte, 112+24)
	binary.LittleEndian.PutUint32(buf[100:], 1) // PropertyCount
	binary.LittleEndian.PutUint32(buf[104:], 1) // TopLevelPropertyCount
	p := 112
	binary.LittleEndian.PutUint16(buf[p+8:], tdhInTypeUInt32)
	binary.LittleEndian.PutUint16(buf[p+16:], 1)

	s := analyzeSchema(buf)
	if s.state != schemaFailed {
		t.Errorf("未知属性名应 schemaFailed，得到 %d", s.state)
	}
}

func TestAnalyzeSchemaParamFallsToSlow(t *testing.T) {
	// 含参数化属性（PropertyParamCount）→ 静态布局算不了 → 慢路径
	const nameBase = 112 + 24
	buf := make([]byte, nameBase+8)
	putU32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(buf[off:], v) }
	putU16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(buf[off:], v) }

	putU32(100, 1) // PropertyCount
	putU32(104, 1) // TopLevelPropertyCount
	p := 112
	putU32(p, propertyParamCount) // Flags
	putU32(p+4, uint32(nameBase))
	putU16(p+8, tdhInTypeUInt32)
	putU16(p+16, 1)
	for i, c := range "pid\x00" {
		putU16(nameBase+i*2, uint16(c))
	}

	s := analyzeSchema(buf)
	// 只有 pid 没有 size 属性名 → failed；
	// 若两个名字都在而布局参数化 → slow。这里验证 failed 分支不 panic 即可。
	if s.state != schemaFailed && s.state != schemaSlow {
		t.Errorf("参数化属性应 failed 或 slow，得到 %d", s.state)
	}
}

func toUint16(b []byte) []uint16 {
	out := make([]uint16, len(b)/2)
	for i := range out {
		out[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return out
}

// ---- aggregator：tracked-set 语义（方案 §4.5 / §8）----

func TestAggregatorTrackedSetSemantics(t *testing.T) {
	base := time.Unix(0, 0)
	now := base
	agg := newAggregator(func() time.Time { return now })

	// 未登记前的事件必须被丢弃
	agg.add(100, kindTCP_RX, 999)
	if rx, tx, _ := agg.get(100); rx != 0 || tx != 0 {
		t.Fatalf("登记前的事件不应计入：rx=%d tx=%d", rx, tx)
	}

	// 登记（首次 get 返回 0 基线）之后事件才开始累计
	agg.add(100, kindTCP_RX, 100)
	agg.add(100, kindTCP_TX, 200)
	agg.add(100, kindUDP_RX, 300)
	agg.add(100, kindUDP_TX, 400)
	rx, tx, ok := agg.get(100)
	if !ok || rx != 400 || tx != 600 {
		t.Fatalf("rx=%d tx=%d ok=%v，应为 400/600", rx, tx, ok)
	}

	// 未登记的 PID：事件丢弃 + get 首问登记 0 基线
	agg.add(200, kindTCP_RX, 500)
	if rx, _, _ := agg.get(200); rx != 0 {
		t.Fatalf("未登记 PID 的事件不应计入，rx=%d", rx)
	}
	agg.add(200, kindTCP_RX, 50)
	if rx, _, _ := agg.get(200); rx != 50 {
		t.Fatalf("登记后应累计，rx=%d", rx)
	}
}

func TestAggregatorTTLPrune(t *testing.T) {
	base := time.Unix(0, 0)
	now := base
	agg := newAggregator(func() time.Time { return now })

	agg.get(100) // 登记
	agg.add(100, kindTCP_RX, 10)
	now = now.Add(trackedTTL + time.Second)
	agg.get(200) // 触发 prune 扫描（超过 pruneInterval）

	// 100 已超 TTL：条目被淘汰，事件不再计入；再次 get 重新登记 0 基线
	agg.add(100, kindTCP_RX, 10)
	if rx, _, _ := agg.get(100); rx != 0 {
		t.Fatalf("TTL 淘汰后应重新登记 0 基线，rx=%d", rx)
	}

	// 常问的 200 不应被淘汰
	for i := 0; i < 5; i++ {
		now = now.Add(5 * time.Second)
		agg.get(200)
	}
	agg.add(200, kindUDP_TX, 7)
	if _, tx, _ := agg.get(200); tx != 7 {
		t.Fatalf("常问的 PID 不应被 TTL 淘汰，tx=%d", tx)
	}
}

// TestAggregatorConcurrentAddGet 把 ETW 回调线程与采样器的对撞跑出来。
//
// 存在的理由很具体：add 曾在锁**外**读 counters，而 get 在锁内插入、pruneLocked
// 在锁内删除——`fatal error: concurrent map read and map write`，且是 fatal 不是
// panic，callback 里的 recover 拦不住。其余 12 个测试全是单线程的，谁也照不到这里。
//
// ⚠️ 这个测试只有带 -race 才有意义，而本机的 -race **必须在 PowerShell 下跑**
// （Git Bash 里 ThreadSanitizer 启动即分配失败）。见 docs/WINNET_ETW_TODO.md §6。
func TestAggregatorConcurrentAddGet(t *testing.T) {
	agg := newAggregator(nil)
	const pids = 16

	var readers sync.WaitGroup
	stop := make(chan struct{})
	writerDone := make(chan struct{})

	// 写侧：模拟 ProcessTrace 回调线程（串行，但与读侧并发）
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for pid := uint32(1); pid <= pids; pid++ {
				agg.add(pid, kindTCP_RX, 1)
				agg.add(pid, kindUDP_TX, 1)
			}
		}
	}()

	// 读侧：模拟采样器的 Bytes()——首问会插入新条目，正是与写侧相撞的那一步
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 500; j++ {
				for pid := uint32(1); pid <= pids; pid++ {
					agg.get(pid)
				}
			}
		}()
	}

	readers.Wait()
	close(stop)
	<-writerDone

	// 登记之后必然累计过（写侧一直在打），顺带确认没被并发弄丢
	if rx, tx, ok := agg.get(1); !ok || rx == 0 || tx == 0 {
		t.Fatalf("并发后计数异常：rx=%d tx=%d ok=%v", rx, tx, ok)
	}
}

// ---- 会话存活语义 ----

// TestBytesDeadSession：会话没了要报 ok=false，而不是一个不再增长的累计值。
// 后者会被采样器差分成恒 0，前端画出贴底实线（RESOURCE_RATE_CHART_PLAN §4.4
// 要求采不到必须是 null）。见 docs/WINNET_ETW_TODO.md §2.2。
func TestBytesDeadSession(t *testing.T) {
	done := make(chan struct{})
	c := &Collector{
		agg:     newAggregator(nil),
		sess:    &etwSession{consumerDone: done, traceHandle: invalidProcessTraceHandle},
		schemas: make(schemaCache),
	}

	if _, _, ok := c.Bytes(1234); !ok {
		t.Fatal("会话存活时应返回 ok=true")
	}

	close(done) // ProcessTrace 返回：被抢走 session 名，或已停
	if _, _, ok := c.Bytes(1234); ok {
		t.Fatal("会话已终止时必须返回 ok=false（否则前端画成恒 0 实线）")
	}

	// Close 之后同样是 ok=false（此路径不碰真实 ETW 句柄）
	c.closed.Store(true)
	if _, _, ok := c.Bytes(1234); ok {
		t.Fatal("Close 之后必须返回 ok=false")
	}
}

func TestBytesRejectsInvalidPID(t *testing.T) {
	c := &Collector{
		agg:     newAggregator(nil),
		sess:    &etwSession{consumerDone: make(chan struct{}), traceHandle: invalidProcessTraceHandle},
		schemas: make(schemaCache),
	}
	for _, pid := range []int32{0, -1} {
		if _, _, ok := c.Bytes(pid); ok {
			t.Fatalf("pid=%d 应返回 ok=false", pid)
		}
	}
	var nilC *Collector
	if _, _, ok := nilC.Bytes(1); ok {
		t.Fatal("nil Collector 应返回 ok=false 且不 panic")
	}
}

// TestCloseTraceSucceeded：ERROR_CTX_CLOSE_PENDING 是实时消费下 CloseTrace 的
// 成功返回，当成失败会让每次退出都假报警（验收项 #10 假红）。
func TestCloseTraceSucceeded(t *testing.T) {
	cases := []struct {
		code uint32
		want bool
	}{
		{0, true},
		{errCtxClosePending, true}, // 7007
		{5, false},                 // ERROR_ACCESS_DENIED
		{6, false},                 // ERROR_INVALID_HANDLE
	}
	for _, c := range cases {
		if got := closeTraceSucceeded(c.code); got != c.want {
			t.Fatalf("closeTraceSucceeded(%d) = %v，应为 %v", c.code, got, c.want)
		}
	}
}

// TestControlStopSucceeded：「查不到该 session」是 4201 不是 4200，而且
// STOP 成功停掉会话之后返回的**也是** 4201（2026-09-07 真机实测）。
// 把它当失败会让每次退出都假报警。见 docs/WINNET_ETW_TODO.md §2.7。
func TestControlStopSucceeded(t *testing.T) {
	cases := []struct {
		code uint32
		want bool
	}{
		{0, true},
		{errWmiInstanceNotFound, true}, // 4201：实测 STOP 成功也返回它
		{errWmiGuidNotFound, true},     // 4200
		{errAccessDenied, false},       // 5
		{24, false},                    // ERROR_BAD_LENGTH：缓冲不够，是真失败
	}
	for _, c := range cases {
		if got := controlStopSucceeded(c.code); got != c.want {
			t.Fatalf("controlStopSucceeded(%d) = %v，应为 %v", c.code, got, c.want)
		}
	}
	if errWmiInstanceNotFound != 4201 || errWmiGuidNotFound != 4200 {
		t.Fatal("WMI 错误码写错了：INSTANCE_NOT_FOUND=4201, GUID_NOT_FOUND=4200")
	}
}

// TestTranslateEnableError：普通用户的实际降级点在 EnableTraceEx2，
// 文案必须点出「权限」，否则日志里只有 win32 error 5。见 TODO §2.8。
func TestTranslateEnableError(t *testing.T) {
	err := translateEnableError(errAccessDenied)
	if err == nil || !strings.Contains(err.Error(), "权限") {
		t.Fatalf("ACCESS_DENIED 的文案应点出权限，实际：%v", err)
	}
	if err := translateEnableError(87); err == nil || !strings.Contains(err.Error(), "87") {
		t.Fatalf("其它错误码应原样带出，实际：%v", err)
	}
}

// TestBuildPropertiesBufferIndependent：残留清理路径要用独立缓冲。
// ControlTraceW(STOP) 返回时会改写传入的 properties，复用同一块去重试
// StartTraceW 就不再是原始参数了（docs/WINNET_ETW_TODO.md §2.4）。
func TestBuildPropertiesBufferIndependent(t *testing.T) {
	name := utf16FromString(sessionName)
	a := buildPropertiesBuffer(name)
	b := buildPropertiesBuffer(name)
	if &a[0] == &b[0] {
		t.Fatal("两次调用必须返回各自独立的缓冲")
	}

	// 模拟 ControlTraceW 写回 out 字段，确认不会渗到另一块
	pa := (*eventTraceProperties)(unsafe.Pointer(&a[0]))
	pa.EventsLost = 42
	pa.LoggerNameOffset = 0xDEAD
	pb := (*eventTraceProperties)(unsafe.Pointer(&b[0]))
	if pb.EventsLost != 0 || pb.LoggerNameOffset != uint32(unsafe.Sizeof(eventTraceProperties{})) {
		t.Fatalf("第二块缓冲被污染：EventsLost=%d LoggerNameOffset=%d",
			pb.EventsLost, pb.LoggerNameOffset)
	}
}

// ---- utf16 工具 ----

func TestUtf16NameBytes(t *testing.T) {
	b := utf16NameBytes("pid")
	if len(b) != 8 { // 3 字符 + 终止符 = 4 个 UTF-16 码元
		t.Fatalf("长度 %d，应为 8", len(b))
	}
	if binary.LittleEndian.Uint16(b[6:]) != 0 {
		t.Error("应以 UTF-16 NUL 结尾")
	}
	if binary.LittleEndian.Uint16(b) != 'p' {
		t.Error("首码元应为 'p'")
	}
}
