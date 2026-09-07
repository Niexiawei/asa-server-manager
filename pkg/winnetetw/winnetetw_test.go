//go:build windows

package winnetetw

import (
	"encoding/binary"
	"runtime"
	"strings"
	"sync"
	"syscall"
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
		{"etwBufferContext", unsafe.Sizeof(etwBufferContext{}), 4}, // 2 是错的，见该类型注释
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

		// eventRecord：callback 入参，UserContext 在 104。
		// ⚠️ ETW_BUFFER_CONTEXT 是 **4 字节**（ProcessorNumber+Alignment+LoggerId(USHORT)），
		// 所以 ExtendedDataCount 在 84、UserDataLength 在 **86**。
		// 这两个数曾经被写成 82/84（把 BufferContext 当成 2 字节），后果是
		// payload 长度实际读到 ExtendedDataCount，恒为 0 → 每个事件都解析失败。
		{"rec.EventHeader", unsafe.Offsetof(rec.EventHeader), 0},
		{"rec.BufferContext", unsafe.Offsetof(rec.BufferContext), 80},
		{"rec.ExtendedDataCount", unsafe.Offsetof(rec.ExtendedDataCount), 84},
		{"rec.UserDataLength", unsafe.Offsetof(rec.UserDataLength), 86},
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
	if v, _ := agg.get(100); v.Rx() != 0 || v.Tx() != 0 {
		t.Fatalf("登记前的事件不应计入：rx=%d tx=%d", v.Rx(), v.Tx())
	}

	// 登记（首次 get 返回 0 基线）之后事件才开始累计
	agg.add(100, kindTCP_RX, 100)
	agg.add(100, kindTCP_TX, 200)
	agg.add(100, kindUDP_RX, 300)
	agg.add(100, kindUDP_TX, 400)
	v, ok := agg.get(100)
	if !ok || v.Rx() != 400 || v.Tx() != 600 {
		t.Fatalf("rx=%d tx=%d ok=%v，应为 400/600", v.Rx(), v.Tx(), ok)
	}
	// 四路必须各归各位——聚合值对了不代表分项对了
	if v.TCPRx != 100 || v.TCPTx != 200 || v.UDPRx != 300 || v.UDPTx != 400 {
		t.Fatalf("分项错位：%+v", v)
	}

	// 未登记的 PID：事件丢弃 + get 首问登记 0 基线
	agg.add(200, kindTCP_RX, 500)
	if v, _ := agg.get(200); v.Rx() != 0 {
		t.Fatalf("未登记 PID 的事件不应计入，rx=%d", v.Rx())
	}
	agg.add(200, kindTCP_RX, 50)
	if v, _ := agg.get(200); v.Rx() != 50 {
		t.Fatalf("登记后应累计，rx=%d", v.Rx())
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
	if v, _ := agg.get(100); v.Rx() != 0 {
		t.Fatalf("TTL 淘汰后应重新登记 0 基线，rx=%d", v.Rx())
	}

	// 常问的 200 不应被淘汰
	for i := 0; i < 5; i++ {
		now = now.Add(5 * time.Second)
		agg.get(200)
	}
	agg.add(200, kindUDP_TX, 7)
	if v, _ := agg.get(200); v.Tx() != 7 {
		t.Fatalf("常问的 PID 不应被 TTL 淘汰，tx=%d", v.Tx())
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
	if v, ok := agg.get(1); !ok || v.Rx() == 0 || v.Tx() == 0 {
		t.Fatalf("并发后计数异常：rx=%d tx=%d ok=%v", v.Rx(), v.Tx(), ok)
	}
}

// TestEventCallbackThunk 直接调用 syscall.NewCallback 造出来的那个函数指针，
// 用一条**合成的** EVENT_RECORD 走一遍回调链路。
//
// 这是唯一能在没有管理员权限、没有真实 ETW 会话的情况下验证的一段：
// 回调能不能被调进来、能不能从 UserContext 还原出 Collector、
// 事件计数有没有加上、解析失败会不会把进程带崩。
// 真实事件的解析仍然只能靠真机（§8 的既有裁决）。
func TestEventCallbackThunk(t *testing.T) {
	c := &Collector{
		agg:     newAggregator(nil),
		sess:    &etwSession{consumerDone: make(chan struct{}), traceHandle: invalidProcessTraceHandle},
		schemas: make(schemaCache),
	}

	// 预置 schema，绕开 TDH：这里要测的是「回调 → 分类 → 按 offset 读 payload
	// → 落到计数」这条链，而合成记录没有真实的 manifest。
	// （TDH 本身的调用约定由 TestTdhGetEventInformationDoesNotCrash 覆盖。）
	c.schemas[10] = &eventSchema{state: schemaFast, pidOffset: 0, pidSize: 4, sizeOffset: 4, sizeSize: 4}

	const testPID = 4321
	c.BytesByProtocol(testPID) // 先登记，否则事件按设计被丢弃

	payload := make([]byte, 32)
	binary.LittleEndian.PutUint32(payload[0:], testPID)
	binary.LittleEndian.PutUint32(payload[4:], 1500) // size

	rec := eventRecord{
		UserData:       unsafe.Pointer(&payload[0]),
		UserDataLength: uint16(len(payload)),
		UserContext:    unsafe.Pointer(c),
	}
	rec.EventHeader.EventDescriptor.Id = 10 // TCP IPv4 send

	// 与 ETW 完全一样的调用方式：拿函数指针，传一个 EVENT_RECORD*
	r, _, _ := syscall.SyscallN(getEventCallback(), uintptr(unsafe.Pointer(&rec)))
	if r != 0 {
		t.Errorf("回调应返回 0，实际 %d", r)
	}
	if got := c.eventsReceived.Load(); got != 1 {
		t.Errorf("事件计数应为 1，实际 %d——回调没能从 UserContext 还原出 Collector？", got)
	}
	if got := c.parseDropped.Load(); got != 0 {
		t.Errorf("预置 schema 下不该解析失败，parseDropped=%d", got)
	}

	// 这一条就是 UserDataLength 偏移写错时会挂掉的断言：payload 长度读成
	// ExtendedDataCount（恒 0）的话，读取越界 → 解析失败 → 这里恒为 0。
	v, ok := c.BytesByProtocol(testPID)
	if !ok || v.TCPTx != 1500 {
		t.Errorf("事件应落到 TCP 发送方向 1500 字节，实际 %+v ok=%v", v, ok)
	}

	// 不认识的 Event ID 直接丢弃，连计数都不该加
	rec.EventHeader.EventDescriptor.Id = 9999
	before := c.eventsReceived.Load()
	syscall.SyscallN(getEventCallback(), uintptr(unsafe.Pointer(&rec)))
	if c.eventsReceived.Load() != before {
		t.Error("未登记的 Event ID 不该被计数")
	}

	// nil 记录不能把进程带崩（callback 里的 recover 是最后一道，但不该走到）
	syscall.SyscallN(getEventCallback(), 0)
	runtime.KeepAlive(payload)
}

// TestTdhGetEventInformationDoesNotCrash 用一条合成记录调真正的 TDH。
//
// 这条测试存在的唯一理由是那次真机崩溃：`TdhGetEventInformation` 的
// 最后一个参数是 ULONG*（in/out），原来按值传了 len(buffer)，TDH 把 4096
// 当指针解引用 → 回调线程 0xc0000005 → **整个进程没了**。
// 调用约定写错时这条测试会同样崩掉，所以它比任何断言都有效。
// 合成记录没有 manifest，TDH 只会返回一个错误码，那正是期望结果。
func TestTdhGetEventInformationDoesNotCrash(t *testing.T) {
	payload := make([]byte, 32)
	rec := eventRecord{
		UserData:       unsafe.Pointer(&payload[0]),
		UserDataLength: uint16(len(payload)),
	}
	rec.EventHeader.EventDescriptor.Id = 10

	if _, err := getEventInformation(&rec); err == nil {
		t.Log("合成记录居然解析出了 schema（无妨，本测试只要求不崩）")
	}

	// 慢路径同理：TdhGetPropertySize / TdhGetProperty 的参数个数与顺序写错
	// 同样是解引用一个整数。
	name := utf16NameBytes("PID")
	if v, ok := tdhPropertyValue(&rec, name); ok {
		t.Logf("合成记录取到了 PID=%d（无妨）", v)
	}
	runtime.KeepAlive(payload)
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

// TestBytesByProtocolSharesRegistration：两个出口必须走同一条登记路径，
// 否则「先问分项再问聚合」会登记两次、语义分岔。
func TestBytesByProtocolSharesRegistration(t *testing.T) {
	c := &Collector{
		agg:     newAggregator(nil),
		sess:    &etwSession{consumerDone: make(chan struct{}), traceHandle: invalidProcessTraceHandle},
		schemas: make(schemaCache),
	}

	v, ok := c.BytesByProtocol(4321) // 首问 = 登记 + 0 基线
	if !ok || v != (ProtoBytes{}) {
		t.Fatalf("首问应是零基线，实际 %+v ok=%v", v, ok)
	}

	c.agg.add(4321, kindUDP_RX, 700)
	c.agg.add(4321, kindTCP_TX, 30)

	v, _ = c.BytesByProtocol(4321)
	if v.UDPRx != 700 || v.TCPTx != 30 || v.TCPRx != 0 || v.UDPTx != 0 {
		t.Fatalf("分项不对：%+v", v)
	}
	rx, tx, _ := c.Bytes(4321)
	if rx != v.Rx() || tx != v.Tx() {
		t.Fatalf("Bytes 应等于分项之和：%d/%d vs %d/%d", rx, tx, v.Rx(), v.Tx())
	}

	// 会话没了：两个出口都要报采不到
	c.closed.Store(true)
	if _, ok := c.BytesByProtocol(4321); ok {
		t.Fatal("Close 之后 BytesByProtocol 必须返回 ok=false")
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

// TestControlCodeSemantics 用**真实的会话**验证两个控制码的语义，
// 而不是断言我们以为的数值。
//
// 这条测试是为一次具体事故写的：QUERY 与 STOP 曾被写反（QUERY=0/STOP=1 才对），
// 于是 Describe() 里的一次「查询」实际把自己的会话停掉了，而 stop() 里的
// 「停止」实际只是查询、会话一直泄漏。数值断言防不住这种错——它和被测代码
// 出自同一个错误认知——只有让真会话跑一遍才防得住。
//
// 非提权也能跑：StartTraceW 不需要管理员（要管理员的是 EnableTraceEx2）。
func TestControlCodeSemantics(t *testing.T) {
	name := utf16FromString(sessionName)
	ctl := func(code uint32) uint32 {
		buf := buildPropertiesBuffer(name)
		return controlTraceW(0, &name[0], (*eventTraceProperties)(unsafe.Pointer(&buf[0])), code)
	}

	// 先确保没有残留（别把正在跑的 asa-server 的会话搅了：有的话直接跳过）
	if ctl(eventTraceControlQuery) == 0 {
		t.Skip("已有同名 ETW 会话在跑，跳过以免影响它")
	}

	var h traceHandle
	props := buildPropertiesBuffer(name)
	if rc := startTraceW(&h, &name[0], (*eventTraceProperties)(unsafe.Pointer(&props[0]))); rc != 0 {
		t.Skipf("建不了 ETW 会话（win32 %d），跳过", rc)
	}
	t.Cleanup(func() { _ = ctl(eventTraceControlStop) })

	// QUERY 必须是**只读**的：连问两次，会话都得还在
	if rc := ctl(eventTraceControlQuery); rc != 0 {
		t.Fatalf("QUERY 第一次返回 %d，应为 0", rc)
	}
	if rc := ctl(eventTraceControlQuery); rc != 0 {
		t.Fatalf("QUERY 第二次返回 %d —— 说明它把会话停掉了，两个控制码写反了", rc)
	}

	// STOP 必须真的停掉：之后 QUERY 应报「查不到」
	if rc := ctl(eventTraceControlStop); !controlStopSucceeded(rc) {
		t.Fatalf("STOP 返回 %d", rc)
	}
	if rc := ctl(eventTraceControlQuery); rc != errWmiInstanceNotFound {
		t.Fatalf("STOP 之后 QUERY 返回 %d，应为 %d（会话没被真正停掉）", rc, errWmiInstanceNotFound)
	}
}

// TestControlStopSucceeded：「查不到该 session」是 4201 不是 4200；
// 停一个已经不在的会话不算失败，当失败会让每次退出都假报警。
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
