//go:build windows

package winnetetw

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// etwSession 封装一次 ETW 实时会话的完整生命周期（方案 §4.1）：
//
//	StartTraceW（固定名，残留清理）→ EnableTraceEx2（Event ID 过滤）
//	→ OpenTraceW（EventRecordCallback + Context）→ ProcessTrace（阻塞消费）
//	→ Close：CloseTrace → 等 ProcessTrace 返回 → ControlTraceW(STOP)
//
// Stop 顺序不能乱：CloseTrace 让 ProcessTrace 返回，STOP 才真正销毁 session；
// 反过来的话 ProcessTrace 可能拿着已销毁的 session 读。
type etwSession struct {
	sessionHandle traceHandle // StartTraceW 返回（controller 侧）
	traceHandle   traceHandle // OpenTraceW 返回（consumer 侧）

	nameUTF16 []uint16 // session 名，指针会被 ETW 持有，必须存活整个会话
	propsBuf  []byte   // StartTraceW 用过的 properties 缓冲，保活 + 偏移复用
	filterBuf []byte   // Event ID 过滤器，EnableTraceEx2 之后保守保活

	consumerDone chan struct{}
	closeOnce    sync.Once
	closeErr     error
}

// 会话参数（方案 §12：先写死，压测发现问题再提到 Options，不加无消费者的配置面）。
const (
	bufferSizeKB      = 64 // 每 buffer 64KB
	minimumBuffers    = 64 // 下限，保证高流量时 consumer 不至于饿死
	maximumBuffers    = 128
	flushTimerSec     = 1 // 1 秒强制 flush，实时性优先
	traceLevelVerbose = 5 // TRACE_LEVEL_VERBOSE：宁多勿漏，内核侧还有 Event ID 过滤兜底
)

// buildPropertiesBuffer 拼出「结构体 + LoggerName + 空文件名」的连续缓冲。
// EVENT_TRACE_PROPERTIES 要求两个字符串紧跟结构体、偏移写在字段里——
// 实时会话不落盘，LogFileNameOffset 指向一个空串（官方文档允许且必须给合法指针）。
func buildPropertiesBuffer(nameUTF16 []uint16) []byte {
	const propsSize = unsafe.Sizeof(eventTraceProperties{})
	nameBytes := len(nameUTF16) * 2
	buf := make([]byte, propsSize+uintptr(nameBytes)+2) // +2：空文件名的 L'\0'

	props := (*eventTraceProperties)(unsafe.Pointer(&buf[0]))
	props.LoggerNameOffset = uint32(propsSize)
	props.LogFileNameOffset = uint32(propsSize) + uint32(nameBytes)
	props.BufferSize = bufferSizeKB
	props.MinimumBuffers = minimumBuffers
	props.MaximumBuffers = maximumBuffers
	props.FlushTimer = flushTimerSec
	props.LogFileMode = eventTraceRealTimeMode

	props.Wnode.BufferSize = uint32(len(buf))
	props.Wnode.Flags = wnodeFlagTracedGuid
	props.Wnode.ClientContext = 1 // QPC 时钟

	copy(buf[propsSize:], unsafe.Slice((*byte)(unsafe.Pointer(&nameUTF16[0])), nameBytes))
	// 文件名区已经是零字节，即空字符串
	return buf
}

func utf16FromString(s string) []uint16 {
	return utf16.Encode([]rune(s + "\x00"))
}

// buildEventIDFilter 拼出 EVENT_FILTER_EVENT_ID：
// 头部 4 字节（FilterIn=1 + Reserved + Count）+ Count 个 uint16 Event ID。
// 直接按字节拼而不用结构体——ANYSIZE_ARRAY 的 sizeof 语义太容易写错。
func buildEventIDFilter() []byte {
	buf := make([]byte, eventFilterEventIDHeaderSize+len(kernelNetworkEvents)*2)
	buf[0] = 1 // FilterIn = TRUE：只收这些 ID
	binary.LittleEndian.PutUint16(buf[2:4], uint16(len(kernelNetworkEvents)))
	i := eventFilterEventIDHeaderSize
	for id := range kernelNetworkEvents {
		binary.LittleEndian.PutUint16(buf[i:i+2], id)
		i += 2
	}
	return buf
}

// startSession 建立 ETW 会话并挂上 provider（含 Event ID 过滤）。
// context 会被写进 EVENT_TRACE_LOGFILEW.Context，经 EVENT_RECORD.UserContext
// 原样回传到 callback——本包用它传 Collector 指针。
// eventCallback 是 syscall.NewCallback 的产物（进程内只该创建一次，见 collector.go）。
func startSession(context unsafe.Pointer, eventCallback uintptr) (*etwSession, error) {
	s := &etwSession{
		nameUTF16:    utf16FromString(sessionName),
		traceHandle:  invalidProcessTraceHandle,
		consumerDone: make(chan struct{}),
	}

	s.propsBuf = buildPropertiesBuffer(s.nameUTF16)
	namePtr := &s.nameUTF16[0]
	props := (*eventTraceProperties)(unsafe.Pointer(&s.propsBuf[0]))

	// StartTraceW。残留的旧 session（上次崩溃没走到 Stop）先清掉再重试一次，
	// 绝不升级成 AsaServerProcNet-1/2/3（ETW session 是有限系统资源）。
	errCode := startTraceW(&s.sessionHandle, namePtr, props)
	if errCode == errAlreadyExists {
		// destroySession 自带独立缓冲与复核重试：ControlTraceW 返回时会把被停会话的
		// 属性写回传入的缓冲（含两个 NameOffset 与一堆 out 字段），所以重试
		// StartTraceW 之前 propsBuf 也必须重建，否则用的不再是原始参数。
		// 只清自己的名字；清不掉就让下面的 StartTraceW 去报 ALREADY_EXISTS。
		_ = destroySession(s.nameUTF16)

		s.propsBuf = buildPropertiesBuffer(s.nameUTF16)
		props = (*eventTraceProperties)(unsafe.Pointer(&s.propsBuf[0]))
		errCode = startTraceW(&s.sessionHandle, namePtr, props)
	}
	if errCode != 0 {
		return nil, translateStartError(errCode)
	}

	// provider 启用失败也要把 session 停掉，不留僵尸
	if err := s.enableProvider(); err != nil {
		s.stop()
		return nil, err
	}

	// consumer 侧
	logfile := &eventTraceLogfileW{
		LoggerName:          namePtr,
		ProcessTraceMode:    processTraceModeEventRec | processTraceModeRealTime,
		EventRecordCallback: eventCallback,
		Context:             context,
	}
	s.traceHandle = openTraceW(logfile)
	if s.traceHandle == invalidProcessTraceHandle {
		s.stop()
		return nil, errors.New("OpenTraceW 失败（实时消费需要管理员或 Performance Log Users 权限）")
	}

	go func() {
		defer close(s.consumerDone)
		// CloseTrace 之后返回，返回码通常是 ERROR_CANCELLED（1223），属正常退出
		_ = processTrace(&s.traceHandle)
	}()
	return s, nil
}

// enableProvider 启用 Kernel-Network provider，过滤器只放行 8 个 Event ID——
// 这是唯一能在事件产生点之前削减数据量的手段（方案 §4.3）。
func (s *etwSession) enableProvider() error {
	s.filterBuf = buildEventIDFilter()
	filter := eventFilterDescriptor{
		Ptr:  uintptr(unsafe.Pointer(&s.filterBuf[0])),
		Size: uint32(len(s.filterBuf)),
		Type: eventFilterTypeEventID,
	}
	params := enableTraceParameters{
		Version:          enableTraceParametersVersion2,
		EnableFilterDesc: &filter,
		FilterDescCount:  1,
	}
	guid := kernelNetworkProviderGUID
	errCode := enableTraceEx2(s.sessionHandle, &guid, eventControlCodeEnableProvider,
		traceLevelVerbose, 0, 0, 0, &params)
	if errCode != 0 {
		return translateEnableError(errCode)
	}
	return nil
}

// stats 用 ControlTraceW(QUERY) 读会话级丢事件计数。Describe 用；
// QUERY 需要 properties 缓冲不小于启动时的尺寸（同一个构造函数，天然满足）。
func (s *etwSession) stats() (eventsLost, realTimeBuffersLost uint32, err error) {
	buf := buildPropertiesBuffer(s.nameUTF16)
	namePtr := &s.nameUTF16[0]
	props := (*eventTraceProperties)(unsafe.Pointer(&buf[0]))
	if errCode := controlTraceW(0, namePtr, props, eventTraceControlQuery); errCode != 0 {
		return 0, 0, fmt.Errorf("ControlTraceW(QUERY) 失败: win32 error %d", errCode)
	}
	return props.EventsLost, props.RealTimeBuffersLost, nil
}

// destroySession 按名字停掉会话，并**复核它真的没了**。
//
// ⚠️ 单发一次 STOP 不可靠——2026-09-07 实测（Win11，非提权）：
// `ControlTraceW(0, name, STOP)` 返回 4201（按 controlStopSucceeded 属成功语义）
// 之后，会话**有时仍在**（`logman query -ets` 里还是 Running），而且不会自行消失；
// 复现方式是让进程在 STOP 之后立刻退出，残留能存活数分钟，直到下一次 Load 走
// ERROR_ALREADY_EXISTS 分支把它收掉。ETW session 是有限系统资源，不能指望
// 「下次启动兜底」当作正常路径，所以这里 STOP 之后 QUERY 复核，还在就再来一次。
//
// 每轮之间的 200ms 是给内核收尾留的时间；5 轮 = 最坏 1 秒，只发生在停止路径上。
func destroySession(nameUTF16 []uint16) error {
	namePtr := &nameUTF16[0]
	var last uint32
	for i := 0; i < 5; i++ {
		stopBuf := buildPropertiesBuffer(nameUTF16)
		last = controlTraceW(0, namePtr,
			(*eventTraceProperties)(unsafe.Pointer(&stopBuf[0])), eventTraceControlStop)

		queryBuf := buildPropertiesBuffer(nameUTF16)
		if controlTraceW(0, namePtr,
			(*eventTraceProperties)(unsafe.Pointer(&queryBuf[0])), eventTraceControlQuery) != 0 {
			return nil // QUERY 查不到 = 真的销毁了
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !controlStopSucceeded(last) {
		return fmt.Errorf("ControlTraceW(STOP) 失败: win32 error %d", last)
	}
	return fmt.Errorf("ETW session %s 停止后仍可查询到，可能残留（下次启动会自动清理）", sessionName)
}

// alive 报告消费侧是否还活着。ProcessTrace 返回即 consumerDone 关闭——
// 正常 Close 会走到这里，被同机另一个进程抢走 session 名也会走到这里
// （那一刻起计数就不再增长了）。调用方据此把 Bytes 降级成 ok=false。
func (s *etwSession) alive() bool {
	if s == nil {
		return false
	}
	select {
	case <-s.consumerDone:
		return false
	default:
		return true
	}
}

// closeTraceSucceeded：实时消费下 CloseTrace 返回 ERROR_CTX_CLOSE_PENDING(7007)
// 是**文档规定的成功语义**（调用已受理，ProcessTrace 稍后自行返回），不是失败。
// 当成失败的话每次退出都会打一行「卸载实例级网络监控出错」，真出问题时反而分不出来。
func closeTraceSucceeded(errCode uint32) bool {
	return errCode == 0 || errCode == errCtxClosePending
}

// stop 按方案 §4.1 的顺序关闭。sync.Once 保证重复调用无害。
func (s *etwSession) stop() {
	s.closeOnce.Do(func() {
		if s.traceHandle != invalidProcessTraceHandle {
			if errCode := closeTrace(s.traceHandle); !closeTraceSucceeded(errCode) {
				s.closeErr = fmt.Errorf("CloseTrace 失败: win32 error %d", errCode)
			}
			s.traceHandle = invalidProcessTraceHandle
			<-s.consumerDone // ProcessTrace 退出后才能安全 STOP
		}
		if s.sessionHandle != 0 {
			if err := destroySession(s.nameUTF16); err != nil {
				s.closeErr = err
			}
			s.sessionHandle = 0
		}
	})
}

func (s *etwSession) stopErr() error { return s.closeErr }

// translateStartError 把 StartTraceW 的错误码翻成可行动的中文——
// 这个错误最终会出现在「实例级网络监控未启用」的日志里，是用户排障的第一现场。
func translateStartError(errCode uint32) error {
	switch errCode {
	case errAccessDenied:
		return errors.New("StartTraceW 权限不足（实时 ETW 会话需要管理员或 Performance Log Users 组）")
	case errAlreadyExists:
		return errors.New("同名 ETW session 残留且清理后仍无法启动，请用 `logman query -ets` 检查 AsaServerProcNet")
	default:
		return fmt.Errorf("StartTraceW 失败: win32 error %d", errCode)
	}
}

// translateEnableError 同上，但针对 EnableTraceEx2。
//
// ⚠️ 这条路径**才是**普通用户下的实际降级点：方案 §4.8 当初推断非管理员会卡在
// StartTraceW，实测（2026-09-07，Win11 非提权）不是——StartTraceW 照常成功并真的
// 建出了 session，直到 EnableTraceEx2 挂 Kernel-Network provider 时才 ERROR_ACCESS_DENIED。
// 少了这个翻译，用户在日志里只会看到「win32 error 5」，不知道是权限。
// 见 docs/WINNET_ETW_TODO.md §2.8。
func translateEnableError(errCode uint32) error {
	if errCode == errAccessDenied {
		return errors.New("EnableTraceEx2 权限不足（消费 Kernel-Network 事件需要管理员或 Performance Log Users 组）")
	}
	return fmt.Errorf("EnableTraceEx2(Kernel-Network) 失败: win32 error %d", errCode)
}

// 防呆：syscall.Errno 的 Cancelled 常量供上层判断 ProcessTrace 退出用（当前未导出使用）。
var _ = syscall.Errno(1223)
