//go:build windows

package winnetetw

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ETW/TDH 的 API 在 golang.org/x/sys/windows 里没有封装，这里用 lazy DLL 自行声明。
// 结构体布局是本文件的核心资产：字段顺序与类型必须与 evntrace.h / evntcons.h /
// evntprov.h / tdh.h 逐字段一致（Go 与 C 的 struct 排布规则相同，只要顺序/类型/对齐
// 一致，offset 就一致）。全部关键 offset 在 winnetetw_test.go 里钉死——
// EVENT_TRACE_LOGFILEW.EventRecordCallback 写错位置不是报错，是拿野指针当回调直接崩。
//
// 布局依据（2026-09 逐页核对 Microsoft Learn）：
//   WNODE_HEADER / EVENT_TRACE_PROPERTIES / EVENT_TRACE_LOGFILEW / TRACE_LOGFILE_HEADER
//   / EVENT_TRACE / EVENT_TRACE_HEADER / EVENT_HEADER / EVENT_RECORD / EVENT_DESCRIPTOR
//   / ENABLE_TRACE_PARAMETERS / EVENT_FILTER_DESCRIPTOR / EVENT_FILTER_EVENT_ID
//   / TRACE_EVENT_INFO / EVENT_PROPERTY_INFO

var (
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")
	tdhDll   = windows.NewLazySystemDLL("tdh.dll")

	procStartTraceW    = advapi32.NewProc("StartTraceW")
	procControlTraceW  = advapi32.NewProc("ControlTraceW")
	procEnableTraceEx2 = advapi32.NewProc("EnableTraceEx2")
	procOpenTraceW     = advapi32.NewProc("OpenTraceW")
	procProcessTrace   = advapi32.NewProc("ProcessTrace")
	procCloseTrace     = advapi32.NewProc("CloseTrace")

	procTdhGetEventInformation = tdhDll.NewProc("TdhGetEventInformation")
	procTdhGetProperty         = tdhDll.NewProc("TdhGetProperty")
)

// sessionName 固定，绝不生成带编号的实例（方案 §4.2 / 上游设计文档 §13）：
// ETW session 是有限系统资源，崩溃残留的旧 session 由 Load 启动时清理。
const sessionName = "AsaServerProcNet"

// kernelNetworkProviderGUID 是 Microsoft-Windows-Kernel-Network 的 provider GUID。
var kernelNetworkProviderGUID = windows.GUID{
	Data1: 0x7dd42a49,
	Data2: 0x5329,
	Data3: 0x4832,
	Data4: [8]byte{0x8d, 0xfd, 0x43, 0xd9, 0x79, 0x15, 0x3a, 0x88},
}

// Win32 错误码（不用 syscall.Errno 以外的常量包，就这么几个）。
const (
	errAccessDenied        = 5    // ERROR_ACCESS_DENIED：无管理员/Performance Log Users 权限
	errAlreadyExists       = 183  // ERROR_ALREADY_EXISTS：上次崩溃留下的同名 session
	errInsufficientBuffer  = 122  // ERROR_INSUFFICIENT_BUFFER：TDH 探测缓冲区大小
	errWmiGuidNotFound     = 4200 // ERROR_WMI_GUID_NOT_FOUND
	errWmiInstanceNotFound = 4201 // ERROR_WMI_INSTANCE_NOT_FOUND：ControlTrace 查不到该 session
	errCtxClosePending     = 7007 // ERROR_CTX_CLOSE_PENDING：CloseTrace 在实时消费下的**成功**返回
)

// ⚠️ 「查不到该 session」是 **4201**，不是 4200（4200 是 ERROR_WMI_GUID_NOT_FOUND）。
// 而且实测（2026-09-07，Win11 非提权）：`ControlTraceW(0, name, STOP)` 把会话**成功**
// 停掉之后返回的也是 4201——调用前 QUERY 得 0（在），调用后 QUERY 得 4201（没了），
// 但 STOP 自己报 4201。所以这两个码一律不能当失败，否则每次退出都假报警。
// 见 docs/WINNET_ETW_TODO.md §2.7。
func controlStopSucceeded(errCode uint32) bool {
	return errCode == 0 || errCode == errWmiInstanceNotFound || errCode == errWmiGuidNotFound
}

// EVENT_TRACE_CONTROL_*（ControlTraceW 的 ControlCode）。
const (
	eventTraceControlQuery = 1
	eventTraceControlStop  = 0 // EVENT_TRACE_CONTROL_STOP
)

// EVENT_CONTROL_CODE_ENABLE_PROVIDER（EnableTraceEx2 的 ControlCode）。
const eventControlCodeEnableProvider = 1

// LogFileMode / ProcessTraceMode 常量。
const (
	eventTraceRealTimeMode   = 0x00000100 // EVENT_TRACE_REAL_TIME_MODE（StartTraceW 侧）
	processTraceModeEventRec = 0x10000000 // PROCESS_TRACE_MODE_EVENT_RECORD（新格式回调）
	processTraceModeRealTime = 0x00000100 // PROCESS_TRACE_MODE_REAL_TIME（OpenTraceW 侧）
)

// WNODE_FLAG_TRACED_GUID：Wnode.Flags 的必填值。
const wnodeFlagTracedGuid = 0x00020000

// EVENT_FILTER_TYPE_EVENT_ID：内核侧按 Event ID 过滤（evntprov.h）。
// 注意不是 0x80000001（那是保留值），见 EVENT_FILTER_DESCRIPTOR 文档。
const eventFilterTypeEventID = 0x80000200

// ENABLE_TRACE_PARAMETERS_VERSION_2。
const enableTraceParametersVersion2 = 2

// TDH 输入类型（tdh.h TDH_INTYPE_*，只列用到/需要识别宽度的）。
const (
	tdhInTypeUInt8   = 4
	tdhInTypeUInt16  = 6
	tdhInTypeInt32   = 7
	tdhInTypeUInt32  = 8
	tdhInTypeInt64   = 9
	tdhInTypeUInt64  = 10
	tdhInTypePointer = 16
)

// EVENT_PROPERTY_INFO.Flags（tdh.h PROPERTY_FLAGS_*）。
const (
	propertyStruct     = 0x1 // 属性是结构体：布局不能按「顺序标量对齐」算
	propertyParamLen   = 0x2 // 长度来自另一个属性（参数化）：同上
	propertyParamCount = 0x4 // 数量来自另一个属性（参数化）：同上
)

// EVENT_FILTER_EVENT_ID 的固定头部长度（FilterIn + Reserved + Count），
// 后跟 Count 个 USHORT Event ID。
const eventFilterEventIDHeaderSize = 4

// traceHandle 即 TRACEHANDLE（REGHANDLE）。
type traceHandle uint64

const invalidProcessTraceHandle = ^traceHandle(0)

// ---- 结构体定义（顺序与类型 = 布局，勿动） ----

// WNODE_HEADER，48 字节。BufferSize 必须是「结构体 + 后跟字符串」的总字节数。
type wnodeHeader struct {
	BufferSize        uint32
	ProviderId        uint32
	HistoricalContext uint64
	TimeStamp         int64 // union: KernelHandle / TimeStamp
	Guid              windows.GUID
	ClientContext     uint32 // 时钟类型：1 = QPC
	Flags             uint32
}

// eventTraceProperties，120 字节。StartTraceW/ControlTraceW 用的会话属性；
// 调用方在结构体后面拼上 LoggerName 与 LogFileName 两个 UTF-16 字符串，
// 偏移写在 LoggerNameOffset / LogFileNameOffset 里。
type eventTraceProperties struct {
	Wnode               wnodeHeader
	BufferSize          uint32 // 每 buffer 的 KB 数
	MinimumBuffers      uint32
	MaximumBuffers      uint32
	MaximumFileSize     uint32
	LogFileMode         uint32
	FlushTimer          uint32 // 秒
	EnableFlags         uint32
	AgeLimit            int32  // union: AgeLimit / FlushThreshold
	NumberOfBuffers     uint32 // out
	FreeBuffers         uint32 // out
	EventsLost          uint32 // out：session 级丢事件计数（Describe 用）
	BuffersWritten      uint32 // out
	LogBuffersLost      uint32 // out
	RealTimeBuffersLost uint32 // out：实时 consumer 跟不上丢的 buffer 数
	LoggerThreadId      windows.Handle
	LogFileNameOffset   uint32
	LoggerNameOffset    uint32
}

// eventTraceHeader，48 字节。EVENT_TRACE 的内嵌 header（OpenTraceW 输出用，
// 我们不读其内容，占位即可——但字段必须齐全，否则后续全部错位）。
type eventTraceHeader struct {
	Size           uint16
	FieldTypeFlags uint16
	Version        uint32
	ThreadId       uint32
	ProcessId      uint32
	TimeStamp      int64
	Guid           windows.GUID
	ProcessorTime  uint64 // union: KernelTime/UserTime / ProcessorTime
}

// eventTrace，88 字节。仅作 EVENT_TRACE_LOGFILEW 的 CurrentEvent 占位。
type eventTrace struct {
	Header           eventTraceHeader
	InstanceId       uint32
	ParentInstanceId uint32
	ParentGuid       windows.GUID
	MofData          uintptr
	MofLength        uint32
	_                uint32 // 对齐到 88
}

// systemTime 与 timeZoneInformation 用于 TRACE_LOGFILE_HEADER 尾部占位。
type systemTime struct {
	Year, Month, DayOfWeek, Day, Hour, Minute, Second, Milliseconds uint16
}

type timeZoneInformation struct {
	Bias         int32
	StandardName [32]uint16
	StandardDate systemTime
	StandardBias int32
	DaylightName [32]uint16
	DaylightDate systemTime
	DaylightBias int32
}

// traceLogfileHeader，280 字节。仅整体占位（我们不读其内容，
// 丢事件计数走 ControlTraceW QUERY 的 properties.EventsLost，那个 offset 确定）。
type traceLogfileHeader struct {
	BufferSize         uint32
	Version            uint32
	ProviderVersion    uint32
	NumberOfProcessors uint32
	EndTime            int64
	TimerResolution    uint32
	MaximumFileSize    uint32
	LogFileMode        uint32
	BuffersWritten     uint32
	LogInstanceGuid    windows.GUID // union: StartBuffers/PointerSize/EventsLost/CpuSpeedInMHz
	LoggerName         *uint16
	LogFileName        *uint16
	TimeZone           timeZoneInformation
	_                  uint32 // TimeZone(172B) 结束在 243，BootTime 需 8 对齐 → 244..247 填充
	BootTime           int64
	PerfFreq           int64
	StartTime          int64
	ReservedFlags      uint32
	BuffersLost        uint32
}

// eventTraceLogfileW，448 字节。OpenTraceW 的输入：告诉 ETW 消费哪个 session、
// 用哪种回调、回调上下文是什么。ProcessTraceMode（offset 28 的 union）与
// EventRecordCallback（offset 424 的 union）是本包实际设置的字段。
type eventTraceLogfileW struct {
	LogFileName         *uint16
	LoggerName          *uint16
	CurrentTime         int64
	BuffersRead         uint32
	ProcessTraceMode    uint32 // union: LogFileMode / ProcessTraceMode
	CurrentEvent        eventTrace
	LogfileHeader       traceLogfileHeader
	BufferCallback      uintptr
	BufferSize          uint32
	Filled              uint32
	EventsLost          uint32  // 文档标注 Not used，丢事件看 ControlTraceW QUERY
	EventRecordCallback uintptr // union: EventCallback / EventRecordCallback（syscall.NewCallback 的产物）
	IsKernelTrace       uint32
	_                   uint32         // 对齐：Context 指针需 8 对齐
	Context             unsafe.Pointer // = EVENT_RECORD.UserContext，回传 Collector 指针
}

// eventDescriptor，16 字节（Keyword 是 ULONGLONG）。C 侧在 Task(6..7) 之后
// 无填充——Keyword 恰好从 8 开始，Go 的自动对齐结果一致，勿加显式填充。
type eventDescriptor struct {
	Id      uint16
	Version uint8
	Channel uint8
	Level   uint8
	Opcode  uint8
	Task    uint16
	Keyword uint64
}

// eventHeader，80 字节。ProcessId 在 offset 12（事件发布者进程）——
// 注意：kernel 网络事件的 receive 路径常运行在中断/DPC 上下文，Header.ProcessId
// 不可靠（可能是 System/4），PID 必须从 payload 里解析（etw_parse.go）。
type eventHeader struct {
	Size            uint16
	HeaderType      uint16
	Flags           uint16
	EventProperty   uint16
	ThreadId        uint32
	ProcessId       uint32
	TimeStamp       int64
	ProviderId      windows.GUID
	EventDescriptor eventDescriptor
	ProcessorTime   uint64 // union: KernelTime/UserTime / ProcessorTime
	ActivityId      windows.GUID
}

// etwBufferContext，2 字节。
type etwBufferContext struct {
	ProcessorNumber uint8
	LoggerId        uint8
}

// eventRecord，112 字节。EventRecordCallback 的入参——本包整个热路径的起点。
// ExtendedData 指针由 Go 自动填充对齐（86 → 88），勿加显式填充字段。
// 三个指针字段用 unsafe.Pointer 而非 uintptr：结构体由 OS 分配在 ETW 缓冲区
// （非 Go 堆，GC 不扫），直接存指针既安全又免去 vet 的 uintptr→Pointer 转换告警。
type eventRecord struct {
	EventHeader       eventHeader
	BufferContext     etwBufferContext
	ExtendedDataCount uint16
	UserDataLength    uint16
	ExtendedData      unsafe.Pointer
	UserData          unsafe.Pointer // payload 指针
	UserContext       unsafe.Pointer // = eventTraceLogfileW.Context = Collector 指针
}

// enableTraceParameters，48 字节。EnableTraceEx2 的输入，
// 唯一用途是挂上 Event ID 过滤器（内核侧削减数据量）。
// SourceId(GUID) 从 C 布局的 offset 12 起（GUID 只需 4 对齐），
// EnableFilterDesc 指针由 Go 自动对齐到 32——与 C 一致。
type enableTraceParameters struct {
	Version          uint32
	EnableProperty   uint32
	ControlFlags     uint32
	SourceId         windows.GUID
	EnableFilterDesc *eventFilterDescriptor
	FilterDescCount  uint32
}

// eventFilterDescriptor，16 字节。
type eventFilterDescriptor struct {
	Ptr  uintptr // 指向 EVENT_FILTER_EVENT_ID
	Size uint32
	Type uint32
}

// eventFilterEventID 头部（其后紧跟 Count 个 uint16）。
// Go 侧直接用字节切片拼装（sizeof 语义太容易错），见 buildEventIDFilter。
type eventFilterEventID struct {
	FilterIn uint8
	Reserved uint8
	Count    uint16
}

// traceEventInfo 头部，112 字节，其后是 PropertyCount 个 eventPropertyInfo，
// 再往后是各属性名的 UTF-16 字符串（NameOffset 相对本结构体起始）。
type traceEventInfo struct {
	ProviderGuid          windows.GUID
	EventGuid             windows.GUID
	EventDescriptor       eventDescriptor
	DecodingSource        uint32
	ProviderNameOffset    uint32
	LevelNameOffset       uint32
	ChannelNameOffset     uint32
	KeywordsNameOffset    uint32
	TaskNameOffset        uint32
	OpcodeNameOffset      uint32
	EventMessageOffset    uint32
	ProviderMessageOffset uint32
	BinaryXMLOffset       uint32
	BinaryXMLSize         uint32
	EventNameOffset       uint32 // union: EventNameOffset / ActivityIDNameOffset
	EventAttributesOffset uint32 // union: EventAttributesOffset / RelatedActivityIDNameOffset
	PropertyCount         uint32
	TopLevelPropertyCount uint32
	Flags                 uint32
	// EventPropertyInfoArray[] 紧随其后（offset 112）
}

// eventPropertyInfo，24 字节。Flags 含 Struct/ParamLength/ParamCount 之一的
// 属性不能按「顺序标量对齐」算 offset，只能走 TdhGetProperty 慢路径。
type eventPropertyInfo struct {
	Flags         uint32
	NameOffset    uint32
	InType        uint16 // union nonStructType 的前 2 字节
	OutType       uint16
	MapNameOffset uint32
	count         uint16 // union: count / countPropertyIndex
	length        uint16 // union: length / lengthPropertyIndex
	Reserved      uint32
}

// propertyDataDescriptor，16 字节。TdhGetProperty 按属性名取值的输入。
type propertyDataDescriptor struct {
	PropertyName uintptr // LPCWSTR
	ArrayIndex   uint32
	Reserved     uint32
}

// inTypeSize 返回一个标量 InType 的字节宽度；变长类型（字符串/SID/Binary 等）
// 返回 0 表示「静态布局算不了」。数组长度不为 1 的同样由调用方判掉。
func inTypeSize(t uint16) int {
	switch t {
	case tdhInTypeUInt8:
		return 1
	case tdhInTypeUInt16:
		return 2
	case tdhInTypeInt32, tdhInTypeUInt32:
		return 4
	case tdhInTypeInt64, tdhInTypeUInt64, tdhInTypePointer:
		return 8
	default:
		return 0
	}
}

// ---- 原生 API 的薄封装（返回 Win32 错误码，调用方翻译语义） ----

func startTraceW(sessionHandle *traceHandle, name *uint16, props *eventTraceProperties) uint32 {
	r, _, _ := procStartTraceW.Call(
		uintptr(unsafe.Pointer(sessionHandle)),
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(props)))
	return uint32(r)
}

func controlTraceW(handle traceHandle, name *uint16, props *eventTraceProperties, controlCode uint32) uint32 {
	r, _, _ := procControlTraceW.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(props)),
		uintptr(controlCode))
	return uint32(r)
}

func enableTraceEx2(handle traceHandle, providerID *windows.GUID, controlCode uint32,
	level uint8, matchAny, matchAll uint64, timeout uint32, params *enableTraceParameters) uint32 {

	r, _, _ := procEnableTraceEx2.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(providerID)),
		uintptr(controlCode),
		uintptr(level),
		uintptr(matchAny),
		uintptr(matchAll),
		uintptr(timeout),
		uintptr(unsafe.Pointer(params)))
	return uint32(r)
}

func openTraceW(logfile *eventTraceLogfileW) traceHandle {
	r, _, _ := procOpenTraceW.Call(uintptr(unsafe.Pointer(logfile)))
	return traceHandle(r)
}

func processTrace(handle *traceHandle) uint32 {
	r, _, _ := procProcessTrace.Call(
		uintptr(unsafe.Pointer(handle)),
		1, 0, 0)
	return uint32(r)
}

func closeTrace(handle traceHandle) uint32 {
	r, _, _ := procCloseTrace.Call(uintptr(handle))
	return uint32(r)
}

func tdhGetEventInformation(event *eventRecord, buffer []byte) uint32 {
	r, _, _ := procTdhGetEventInformation.Call(
		uintptr(unsafe.Pointer(event)),
		0, 0,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)))
	return uint32(r)
}

// tdhGetProperty 按属性名取值。propertySize 返回实际写入的字节数。
func tdhGetProperty(event *eventRecord, desc *propertyDataDescriptor, buffer []byte, propertySize *uint32) uint32 {
	r, _, _ := procTdhGetProperty.Call(
		uintptr(unsafe.Pointer(event)),
		0, 0,
		1,
		uintptr(unsafe.Pointer(desc)),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(propertySize)))
	return uint32(r)
}
