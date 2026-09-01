#!/bin/sh
# shared-arkapi-probe.sh —— docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md §5.3 的采证脚本。
#
# 用途：在 linux.prefix_mode=shared + linux.allow_shared_arkapi=true 下同时跑两个
# 启用 ArkApi 的实例，把判定 H1/H2/H3/H4 需要的现场一次性抓下来。
#
# 只读：不启动、不停止、不杀任何进程，也不碰配置，可以随便重复跑。
#
# 用法：
#   sudo sh scripts/diag/shared-arkapi-probe.sh [basedir]
#
# 必须 sudo：/proc/<pid>/environ 属于降权用户 asa-umu-runtime，非 root 读不到，
# 而 DISPLAY 比对正是本实验的前提（§5.4）。
#
# 至少跑两次：起完 B 立刻一次（t=0），等满 3 分钟超时再一次（t=180）。
# 两次的差别本身就是证据 —— B 是「慢」还是「挂」，只有对比才分得出来。
#
# 输出同时写到屏幕和 /tmp/shared-arkapi-probe-<时间戳>.txt。

BASEDIR="${1:-/opt/asa-server/basedir}"
OUT="/tmp/shared-arkapi-probe-$(date +%Y%m%d-%H%M%S).txt"

sec() {
	echo
	echo "================================================================"
	echo "== $*"
	echo "================================================================"
}

# descendants 打印 $1 及其全部后代 pid（含自身）。
# 用于把「一次启动」的整条链圈出来：
#   umu-run -> srt-bwrap -> pv-adverb -> proton -> umu.exe -> AsaApiLoader.exe -> GameThread
descendants() {
	_all="$1"
	_frontier="$1"
	while [ -n "$_frontier" ]; do
		_next=""
		for _p in $_frontier; do
			_next="$_next $(ps -o pid= --ppid "$_p" 2>/dev/null)"
		done
		_frontier=$(echo $_next)
		[ -n "$_frontier" ] && _all="$_all $_frontier"
	done
	echo $_all
}

# env_of 打印某个进程的关键环境变量（需要 root）。
env_of() {
	if [ -r "/proc/$1/environ" ]; then
		tr '\0' '\n' < "/proc/$1/environ" 2>/dev/null |
			grep -E '^(DISPLAY|XAUTHORITY|WINEPREFIX|PROTON_VERB|GAMEID|WINEDLLOVERRIDES)=' |
			sed 's/^/      /'
	else
		echo "      （读不到 /proc/$1/environ —— 没用 sudo？）"
	fi
}

# dump_file 打印文件尾部；文件不存在时说明清楚，而不是静默留白。
# 直接写 `tail f | sed || echo` 是不行的：管道的退出码是 sed 的，永远为 0。
dump_file() {
	if [ -r "$1" ]; then
		tail -n "$2" "$1" | sed 's/^/  /'
	else
		echo "  （$1 不存在或不可读）"
	fi
}

main() {

sec "0. 基本情况"
echo "时间        : $(date '+%F %T %z')"
echo "内核        : $(uname -srm)"
echo "发行版      : $(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME")"
echo "当前用户    : $(id -un) (uid=$(id -u))"
echo "BaseDir     : $BASEDIR"
if [ "$(id -u)" -ne 0 ]; then
	echo "!! 不是 root：/proc/*/environ 读不到，DISPLAY 比对会缺失。请用 sudo 重跑。"
fi
echo
echo "-- config.yaml 的 linux.* 关键项 --"
# config.yaml 跟着 exe 走（ASA_CFG > exe 同级 > 系统固定目录），不在 BaseDir 里 ——
# 绿色部署时两者恰好同级，所以只查 BaseDir 会在标准安装下什么也找不到。
CFG=""
for C in "$ASA_CFG" "$BASEDIR/config.yaml" "$BASEDIR/../config.yaml" \
         /opt/asa-server/config.yaml /etc/asa-server/config.yaml; do
	[ -n "$C" ] && [ -r "$C" ] && CFG="$C" && break
done
if [ -n "$CFG" ]; then
	echo "  （来自 $CFG）"
	grep -E '^[[:space:]]+(prefix_mode|allow_shared_arkapi|display|xvfb_bin|xvfb_screen|umu_runtime_user|umu_run_as_root):' \
		"$CFG" | sed 's/^/  /'
else
	echo "  （找不到 config.yaml，试过 ASA_CFG / $BASEDIR / $BASEDIR/.. / /opt/asa-server / /etc/asa-server）"
fi

sec "1. 显示（本实验的前提：所有实例必须落在同一个 DISPLAY 上）"
echo "-- 在跑的 X 服务端 --"
ps -eo pid,user,etimes,args | grep -E '[X]vfb|[X]org|[X]wayland' || echo "  （没有）"
echo
echo "-- /tmp/.X11-unix/ --"
ls -l /tmp/.X11-unix/ 2>/dev/null || echo "  （目录不存在）"
echo
echo "-- 挂载属性（只读会触发 remount 逻辑）--"
findmnt -no SOURCE,FSTYPE,OPTIONS /tmp/.X11-unix 2>/dev/null || echo "  （不是独立挂载点）"
echo
echo "-- {BaseDir}/xvfb.state（自管 Xvfb 的认领文件）--"
if [ -r "$BASEDIR/xvfb.state" ]; then
	sed 's/^/  /' "$BASEDIR/xvfb.state"
else
	echo "  （不存在 —— 说明这次的显示不是自管 Xvfb 提供的）"
fi
echo
echo "-- asa-server verify-arkapi --check-only（[3] 期望报「自管 Xvfb」）--"
ASA_BIN=""
command -v asa-server >/dev/null 2>&1 && ASA_BIN=asa-server
for B in "$BASEDIR/../asa-server" /opt/asa-server/asa-server \
         /usr/local/bin/asa-server /usr/bin/asa-server; do
	[ -n "$ASA_BIN" ] && break
	[ -x "$B" ] && ASA_BIN="$B"
done
if [ -n "$ASA_BIN" ]; then
	"$ASA_BIN" verify-arkapi --check-only 2>&1 | sed 's/^/  /'
else
	echo "  （找不到 asa-server 可执行文件，跳过；手工跑一次补上）"
fi

sec "2. 逐条启动链"
# 每个 umu-run 进程 = 一次实例启动，拿它作为链的根。
LAUNCHES=$(pgrep -f 'umu-launcher/umu-run' 2>/dev/null)
[ -z "$LAUNCHES" ] && echo "没有找到任何 umu-run 进程 —— 没有实例在跑？"

DISPLAYS_SEEN="/tmp/.probe-displays.$$"
SHAPES_SEEN="/tmp/.probe-shapes.$$"
: > "$DISPLAYS_SEEN"
: > "$SHAPES_SEEN"

for ROOT in $LAUNCHES; do
	CMD=$(tr '\0' ' ' < "/proc/$ROOT/cmdline" 2>/dev/null)
	[ -z "$CMD" ] && continue
	EXE=$(echo "$CMD" | grep -oE '/[^ ]*/ShooterGame/Binaries/Win64/[A-Za-z]+\.exe' | head -1)
	MIRROR=${EXE%%/ShooterGame/*}
	LABEL=$(basename "$MIRROR")
	PORT=$(echo "$CMD" | grep -oE '\-Port=[0-9]+' | head -1)

	echo
	echo "----------------------------------------------------------------"
	echo "[启动链] $LABEL  ($PORT)"
	echo "  镜像目录   : $MIRROR"
	echo "  启动的 exe : $(basename "$EXE")"
	echo "  umu-run    : pid=$ROOT  已运行 $(ps -o etimes= -p "$ROOT" 2>/dev/null | tr -d ' ') 秒"
	echo

	PIDS=$(descendants "$ROOT")
	echo "  -- 链上进程（pid / 状态 / 已运行秒 / wchan / comm）--"
	for P in $PIDS; do
		LINE=$(ps -o pid=,stat=,etimes=,wchan:20=,comm= -p "$P" 2>/dev/null)
		[ -n "$LINE" ] && echo "    $LINE"
	done
	echo

	# 关键判据：这条链走到了哪一步。
	HAS_UMUEXE=no; HAS_LOADER=no; HAS_GAME=no
	UMUEXE_PID=""; LOADER_PID=""; GAME_PID=""
	for P in $PIDS; do
		C=$(ps -o comm= -p "$P" 2>/dev/null | tr -d ' ')
		case "$C" in
			umu.exe)         HAS_UMUEXE=yes; UMUEXE_PID="$P" ;;
			# comm 被内核截到 15 字节，AsaApiLoader.exe 会变成 AsaApiLoader.ex
			AsaApiLoader.e*) HAS_LOADER=yes; LOADER_PID="$P" ;;
			GameThread)      HAS_GAME=yes;   GAME_PID="$P" ;;
		esac
	done
	echo "  -- 走到哪一步 --"
	echo "    umu.exe          : $HAS_UMUEXE  $UMUEXE_PID"
	echo "    AsaApiLoader.exe : $HAS_LOADER  $LOADER_PID"
	echo "    GameThread(游戏) : $HAS_GAME  $GAME_PID"
	if [ "$HAS_GAME" = yes ]; then
		echo "    => 完整：游戏进程已存在"
		echo "ok    $LABEL" >> "$SHAPES_SEEN"
	elif [ "$HAS_UMUEXE" = yes ] && [ "$HAS_LOADER" = no ]; then
		echo "    => ★ 止步于 umu.exe、没能 exec 出加载器 —— 这正是 §2.2 记录的挂死形状"
		echo "stuck $LABEL" >> "$SHAPES_SEEN"
	else
		echo "    => 中间态（可能只是还在启动，等 t=180 再看一次）"
		echo "part  $LABEL" >> "$SHAPES_SEEN"
	fi
	echo

	echo "  -- 环境变量（DISPLAY 是本实验的前提）--"
	for P in $ROOT $UMUEXE_PID $LOADER_PID $GAME_PID; do
		echo "    pid $P ($(ps -o comm= -p "$P" 2>/dev/null | tr -d ' ')):"
		env_of "$P"
		D=$(tr '\0' '\n' < "/proc/$P/environ" 2>/dev/null | grep -E '^DISPLAY=' | head -1)
		[ -n "$D" ] && echo "$D" >> "$DISPLAYS_SEEN"
	done
	echo

	if [ -n "$UMUEXE_PID" ] && [ "$HAS_LOADER" = no ]; then
		echo "  -- 卡住的 umu.exe 在等什么 --"
		grep -E '^(State|Threads|SigBlk|SigIgn)' "/proc/$UMUEXE_PID/status" 2>/dev/null | sed 's/^/    /'
		echo "    wchan: $(cat "/proc/$UMUEXE_PID/wchan" 2>/dev/null)"
		if [ -r "/proc/$UMUEXE_PID/stack" ]; then
			echo "    stack:"
			sed 's/^/      /' "/proc/$UMUEXE_PID/stack" 2>/dev/null
		else
			echo "    stack: （读不到，需要 root 且内核开了 CONFIG_STACKTRACE）"
		fi
		echo
	fi

	echo "  -- 这个实例的 ArkApi 日志目录（没建出来本身就是证据）--"
	if [ -d "$MIRROR/ShooterGame/Binaries/Win64/logs" ]; then
		ls -lt "$MIRROR/ShooterGame/Binaries/Win64/logs/" 2>/dev/null | head -10 | sed 's/^/    /'
	else
		echo "    ★ 目录不存在 —— 加载器一个字都没写，与「没有显示时退出码 3」同形"
	fi
	echo
	echo "  -- 这个实例最近的 ShooterGame.log --"
	if [ -d "$MIRROR/ShooterGame/Saved/Logs" ]; then
		ls -lt "$MIRROR/ShooterGame/Saved/Logs/" 2>/dev/null | head -5 | sed 's/^/    /'
	else
		echo "    （没有 Saved/Logs/）"
	fi
	echo

	# launcher.log 是**每实例**的（internal/instance/server.go:61），不在 {BaseDir}/logs 下。
	# 实例名 = 镜像目录名去掉 server-files-tmp- 前缀。
	INST=${LABEL#server-files-tmp-}
	echo "  -- launcher.log（$INST，umu/pressure-vessel/Proton 整条链的输出，最后 40 行）--"
	dump_file "$BASEDIR/instances/$INST/launcher.log" 40 | sed 's/^/  /'
	echo
	echo "  -- 实例目录里的日志文件 --"
	ls -lt "$BASEDIR/instances/$INST/" 2>/dev/null | grep -E '\.log$' | head -6 | sed 's/^/    /'
done

sec "3. 共享会话的全局状态"
echo "-- wineserver（shared 模式下期望恒为 1 个）--"
ps -eo pid,ppid,user,etimes,args | grep -E '[w]ineserver' || echo "  （没有）"
echo "  数量: $(pgrep -cx wineserver 2>/dev/null || echo 0)"
echo
echo "-- explorer.exe / winedevice.exe（desktop 对象的载体）--"
ps -eo pid,ppid,user,args | grep -E '[e]xplorer\.exe|[w]inedevice\.exe' || echo "  （没有）"
echo
echo "-- X 服务端有几个客户端连着 --"
for SOCK in /tmp/.X11-unix/X*; do
	[ -S "$SOCK" ] || continue
	echo "  $SOCK : $(ss -xp 2>/dev/null | grep -c "$SOCK") 条连接"
	ss -xp 2>/dev/null | grep "$SOCK" | sed 's/^/    /'
done
echo
echo "-- Wine prefix --"
ls -ld "$BASEDIR"/umu-prefix* 2>/dev/null | sed 's/^/  /' || echo "  （没有 umu-prefix*）"

sec "4. 日志"
# launcher.log 是每实例的，已在 §2 每条链下面打过；这里只打全局的两份。
echo "-- xvfb.log（自管 Xvfb 的服务端输出，最后 30 行）--"
# 它在降权用户的 home 里（runtimeuser_linux.go:52），不在 BaseDir 根下。
XVFB_LOG=""
for L in "$BASEDIR/runtime-home/xvfb.log" "$BASEDIR/xvfb.log" /root/xvfb.log; do
	[ -r "$L" ] && XVFB_LOG="$L" && break
done
if [ -n "$XVFB_LOG" ]; then
	echo "  （来自 $XVFB_LOG）"
	dump_file "$XVFB_LOG" 30
else
	echo "  （找不到 xvfb.log，试过 $BASEDIR/runtime-home/ 、$BASEDIR/ 、/root/）"
fi
echo
echo "-- asaServer.log 里与本次相关的行 --"
if [ -r "$BASEDIR/logs/asaServer.log" ]; then
	tail -n 400 "$BASEDIR/logs/asaServer.log" |
		grep -E 'allow_shared_arkapi|ArkApi|Xvfb|DISPLAY|显示|启动闸门|prefix' |
		tail -n 40 | sed 's/^/  /'
else
	echo "  （$BASEDIR/logs/asaServer.log 不存在）"
fi

sec "5. 自动小结（判定仍以 §5.4 的表为准）"
echo "-- 各条链看到的 DISPLAY --"
if [ -s "$DISPLAYS_SEEN" ]; then
	sort -u "$DISPLAYS_SEEN" | sed 's/^/  /'
	UNIQ=$(sort -u "$DISPLAYS_SEEN" | wc -l)
	if [ "$UNIQ" -eq 1 ]; then
		echo "  => 所有实例落在同一个显示上：实验前提成立"
	else
		echo "  => ★ 出现了 $UNIQ 个不同的 DISPLAY —— 实验前提【不成立】，"
		echo "     这一轮不能用来判 H1/H2。先查是不是 Xvfb 被看门狗换号重起过（§2.4 路径 1），"
		echo "     或候选链回退到了宿主显示（§2.4 路径 2），修好再重跑。"
	fi
else
	echo "  （一条都没读到 —— 多半是没用 sudo）"
fi
echo
echo "-- 各条链走到的位置 --"
if [ -s "$SHAPES_SEEN" ]; then
	sed 's/^/  /' "$SHAPES_SEEN"
	OKN=$(grep -c '^ok ' "$SHAPES_SEEN")
	STUCKN=$(grep -c '^stuck ' "$SHAPES_SEEN")
	if [ "$OKN" -ge 1 ] && [ "$STUCKN" -ge 1 ]; then
		echo "  => ★ 一条完整 + 一条止步于 umu.exe = §2.2 的原症状。"
		echo "     若此时 DISPLAY 也相同，指向 H1（卡点是 Wine 会话而非显示），走 §7。"
		echo "     但必须等满 3 分钟再跑一次本脚本，确认它是「挂」而不是「慢」。"
	elif [ "$OKN" -ge 2 ]; then
		echo "  => ★ 两条都完整 = 指向 H2/H3（那道闸已被自管 Xvfb 拆掉）。"
		echo "     别急着下结论：还要过 §5.5 的三条补测，"
		echo "     尤其第 1 条「停掉先起的 A 之后 B 是否活着」。"
	fi
else
	echo "  （没有任何启动链）"
fi

rm -f "$DISPLAYS_SEEN" "$SHAPES_SEEN"

echo
echo "================================================================"
echo "采证完毕。原始输出：$OUT"
echo "把它连同两次运行的时间点一起贴回，用于 §5.4 判定。"
echo "================================================================"

}

main 2>&1 | tee "$OUT"
