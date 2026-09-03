#!/bin/sh
# overlay-probe.sh —— docs/UMU_PREFIX_OVERLAY_TODO.md 的采证脚本。
#
# 用途：把判定 overlay 模式各条判据需要的现场一次性抓下来 —— 挂载形态、
# 每个可写层的 dev/ino、wineserver 与它们的对应关系、pfx 软链解析到哪、
# 可写层占用、.lower-stamp 与底层的一致性。
#
# 只读：不挂载、不卸载、不启动、不停止任何东西，可以随便重复跑。
#
# 用法：
#   sudo sh scripts/diag/overlay-probe.sh [basedir]
#
# 必须 sudo：/proc/<pid>/environ 属于降权运行时用户，非 root 读不到，而
# 「哪个 wineserver 挂在哪个前缀上」正是 overlay 全部结论的落点。
#
# 判据里凡是「两个实例两个 wineserver」的，都看第 5 节：socket 目录名
# server-<dev>-<ino> 必须与第 4 节里各自 merged 的 dev/ino 逐位对上。
#
# 输出同时写到屏幕和 /tmp/overlay-probe-<时间戳>.txt。

BASEDIR="${1:-/opt/asa-server/basedir}"
LOWER="$BASEDIR/umu-prefix"
OVROOT="$BASEDIR/umu-prefix-overlay"
OUT="/tmp/overlay-probe-$(date +%Y%m%d-%H%M%S).txt"

sec() {
	echo
	echo "================================================================"
	echo "== $*"
	echo "================================================================"
}

# devino 打印 <dev>-<ino>，格式与 Wine 的 server-<dev>-<ino> 目录名一致
# （十六进制、去前导零）。这是 overlay 方案的承重判据：不同的 inode 才有
# 不同的 wineserver（docs/UMU_PREFIX_OVERLAY_PLAN.md §2）。
devino() {
	[ -e "$1" ] || { echo "(不存在)"; return; }
	stat -L -c '%d %i' "$1" 2>/dev/null | awk '{printf "server-%x-%x\n", $1, $2}'
}

probe() {

sec "0. 基本信息"
date
uname -a
echo "BASEDIR=$BASEDIR"
echo "--- 内核是否支持 overlay ---"
grep -w overlay /proc/filesystems || echo "!! /proc/filesystems 里没有 overlay —— 挂载必然失败，会走降级复制"
echo "--- basedir 所在文件系统（upperdir 必须落在支持的 fs 上）---"
df -Th "$BASEDIR" 2>/dev/null
stat -f -c '%T' "$BASEDIR" 2>/dev/null
echo "--- SELinux（TODO §3.4）---"
command -v getenforce >/dev/null 2>&1 && getenforce || echo "(无 getenforce)"

sec "1. 配置里的 prefix_mode"
grep -nE 'prefix_mode|prefix_dir|runtime_user|display' "$BASEDIR/../config.yaml" 2>/dev/null \
	|| grep -rnE 'prefix_mode|prefix_dir' "$BASEDIR"/../*.yaml 2>/dev/null \
	|| echo "(没找到 config.yaml，用 -h 指定 basedir 的父目录里那个)"

sec "2. 当前的 overlay 挂载"
grep -F "$OVROOT" /proc/self/mountinfo || echo "(没有任何可写层处于挂载状态)"

sec "3. 目录形态"
ls -la "$OVROOT" 2>/dev/null || echo "(没有 $OVROOT)"
echo
echo "底层前缀 $LOWER："
ls -la "$LOWER" 2>/dev/null | head -20
echo "底层 .created-by-proton = $(cat "$LOWER/.created-by-proton" 2>/dev/null || echo '(无)')"

sec "4. 每个可写层：dev/ino、pfx 解析、stamp、占用"
for d in "$OVROOT"/*/; do
	[ -d "$d" ] || continue
	d=${d%/}
	key=$(basename "$d")
	merged="$d/merged"
	echo "---- 实例 $key ----"
	echo "  挂载状态   : $(grep -qF " $merged " /proc/self/mountinfo && echo 已挂载 || echo '未挂载（降级复制形态 或 未启动过）')"
	echo "  merged     : $(devino "$merged")   <- 与第 5 节的 socket 目录名对拍"
	echo "  底层       : $(devino "$LOWER")   <- 必须与上面**不同**，相同即说明退化成共享一个 wineserver"
	if [ -e "$merged/pfx" ] || [ -L "$merged/pfx" ]; then
		echo "  pfx 软链   : $(readlink "$merged/pfx" 2>/dev/null || echo '(不是软链)')  -> 解析到 $(readlink -f "$merged/pfx")"
		echo "  pfx 的 ino : $(devino "$merged/pfx")   <- 必须等于 merged 的，否则 WINEPREFIX 落回底层（§12.8.1）"
	else
		echo "  pfx 软链   : (无) —— 未挂载时 merged 是空目录，属正常；挂载后必须出现"
	fi
	echo "  lower-stamp: $(cat "$d/.lower-stamp" 2>/dev/null || echo '(无)')"
	echo "  upper 占用 : $(du -sh "$d/upper" 2>/dev/null | cut -f1)"
	echo "  upper 条目 : $(find "$d/upper" 2>/dev/null | wc -l) 个（连续两次启动之间不应明显增长，TODO §2.1a）"
	echo "  merged 占用: $(du -sh --one-file-system "$merged" 2>/dev/null | cut -f1)（挂载形态下这是「底层+增量」的视图，不是独占）"
	echo "  work 内容  : $(ls -la "$d/work" 2>/dev/null | sed -n 4p)（内核的，root:root mode 000 属正常）"
done

sec "5. wineserver 与它们持有的前缀"
pgrep -a wineserver 2>/dev/null || echo "(没有 wineserver 在跑)"
for p in $(pgrep wineserver 2>/dev/null); do
	echo "--- pid $p ---"
	tr '\0' '\n' < "/proc/$p/environ" 2>/dev/null | grep -E '^(WINEPREFIX|DISPLAY)=' || echo "  (读不到 environ，忘了 sudo？)"
done
echo
echo "--- wineserver socket 目录（一个目录 = 一个 Wine 会话）---"
ls -la /tmp/.wine-*/ 2>/dev/null || echo "(没有 /tmp/.wine-*)"

sec "6. 游戏进程与显示"
pgrep -a -f 'AsaApiLoader|ArkAscendedServer' 2>/dev/null | head -20 || echo "(没有游戏进程)"
echo "--- Xvfb（N 个 Wine 会话应当共用一个）---"
pgrep -a Xvfb 2>/dev/null || echo "(没有 Xvfb)"
cat "$BASEDIR/xvfb.state" 2>/dev/null && echo

sec "7. 换模式残留（TODO §2.1b/§2.1c 的对象）"
ls -d "$LOWER"-* "$LOWER".bak-* 2>/dev/null | grep -v "^$OVROOT$" || echo "(没有残留目录)"
du -sh "$LOWER" "$LOWER"-* 2>/dev/null | grep -v "$OVROOT"

}

probe 2>&1 | tee "$OUT"
echo
echo "已保存：$OUT"
