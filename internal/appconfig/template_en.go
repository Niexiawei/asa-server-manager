package appconfig

// templateEN is the English, pure-ASCII variant of the config.yaml template.
//
// It exists for terminals / SSH clients that do not decode UTF-8 (e.g. a GBK
// session in Xshell): there, every non-ASCII byte is garbage no matter how the
// file is encoded. See docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-1.2.
//
// WARNING: keep in lockstep with templateZH in template_zh.go -- same keys,
// same values, same order; only the comment language differs.
// TestTemplatesAreEquivalent compares every key, TestEnglishTemplateIsASCII
// rejects any non-ASCII byte. Whether the comments say the same thing is up to
// review.
//
// Placeholders: the first %s is the (already quoted) basedir value, the second
// %s is the trust_local_ca block (per platform, see trustLocalCABlockEN). No
// other % may appear in the template.

// trustLocalCABlockEN renders the two trust_local_ca lines (comment + value).
func trustLocalCABlockEN(goos string) string {
	if goos == "linux" {
		return "    # On Linux the system trust store does not affect browsers (Firefox/Chrome use their own NSS db).\n" +
			"    # Off by default; if needed run `asa-server cert install` (root) and import into the browser manually.\n" +
			"    trust_local_ca: false"
	}
	return "    # Install the self-signed local CA into the Windows trusted root store, so https://localhost:19193 shows no warning\n" +
		"    trust_local_ca: true"
}

const templateEN = `# ASA Server Manager application config
#
# Priority: command-line flag > environment variable ASA_* > this file > built-in defaults
# Env var names: replace dots with underscores and add the ASA_ prefix, e.g. auth.enabled -> ASA_AUTH_ENABLED
#
# This file holds the settings of this program itself. ARK instance settings live in
# instances/<instance name>/instance_config.ini and are unrelated to this file.

# Data directory: empty = same directory as this file (portable default, compatible with all existing installs)
basedir: %s

server:
  port: 19193

  tls:
    # Turning TLS off falls back to HTTP/1.1's "6 connections per origin" limit; long-lived SSE streams will starve REST requests.
    # Browsers only negotiate HTTP/2 via ALPN over TLS; no mainstream browser supports cleartext h2c.
    enabled: true
%s
    # Fill these two if you bring your own certificate (recommended with a domain); no local CA is generated then
    cert_file: ""
    key_file: ""
    # Extra domains added to the certificate SAN (e.g. your reverse proxy's public domain)
    domains: []

  # Sources allowed to set X-Forwarded-For. Empty = trust nobody.
  # gin trusts every proxy by default, which lets any client forge its source IP; this must be tightened.
  trusted_proxies:
    - 127.0.0.1
    - ::1

  cors:
    # Empty = same-origin only (the normal case in production). Only fill this when a reverse-proxy
    # domain needs cross-origin access, e.g. https://ark.example.com
    allowed_origins: []

auth:
  # Master switch. When false the auth middleware is bypassed entirely and auth.db is never opened.
  enabled: false

  database:
    # Empty = {program dir}/database_file/auth.db
    path: ""
    # Apply pending database migrations on startup. If disabled, run manually after upgrades:
    #   asa-server.exe db migrate
    auto_migrate: true

  session:
    ttl: 168h            # login token lifetime
    idle_timeout: 24h    # expire after this much inactivity (sliding renewal; 0 = disabled)
    cookie_name: asa_session
    cookie_path: /
    same_site: lax       # lax | strict | none (none requires TLS)

  # WARNING: LAN auth bypass. Read all three points below before enabling, or auth may be fully open to the internet.
  #
  # 1. The typical deployment runs the reverse proxy (frpc / Nginx) on the same machine, forwarding to 127.0.0.1.
  #    Internet requests and local requests then have exactly the same source IP. The only signal that tells
  #    them apart is whether the proxy sets X-Forwarded-For.
  # 2. So before enabling, make sure your proxy sets XFF:
  #    Nginx  -> proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  #    frpc   -> http-type proxies add it by default; [tcp-type proxies do NOT]
  #    When tunnelling with a frpc tcp-type proxy, lan_bypass MUST stay off, otherwise there is no auth at all.
  # 3. Keep deny_if_forwarded true -- it is what enforces the rule above.
  lan_bypass:
    enabled: false
    networks:
      - 127.0.0.0/8
      - ::1/128
      - 10.0.0.0/8
      - 172.16.0.0/12
      - 192.168.0.0/16
      - 169.254.0.0/16
      - fc00::/7
      - fe80::/10
    deny_if_forwarded: true
    # Additionally trust the subnets this machine's *physical* NICs are currently on (exact CIDR from IP + mask),
    # excluding virtual adapters such as Docker/Hyper-V/WSL2/VPN. Off by default; it supplements the networks
    # list above rather than replacing it. Before enabling, make sure no physical NIC is directly on the internet.
    auto_detect_local_subnets: false

  # Two-step verification (TOTP; works with Google Authenticator / Microsoft Authenticator etc.)
  totp:
    enabled: true      # whether users may enrol two-step verification
    required: false    # true = every user must enrol
    issuer: "ASA Server Manager"
    skew: 1            # allow +/- N 30-second windows to tolerate server clock drift

  password:
    min_length: 8
    bcrypt_cost: 12
    # Note: there is no switch to disable password login. Password is the only way to log in.

  # Login failure rate limiting. Counters are persisted in the database and survive restarts.
  ratelimit:
    max_failures: 5
    window: 15m
    lockout: 15m

  audit:
    max_rows: 2000   # number of audit log rows kept (rolling)

# Global downloader (SteamCMD and other large files; the Linux runtime's umu/GE-Proton/Syncthing downloads share it too)
download:
  # GitHub acceleration proxy, prefix-rewrite style (e.g. https://ghproxy.example.com/), NOT a standard HTTP CONNECT proxy.
  # Only applies to github.com / raw.githubusercontent.com / objects.githubusercontent.com;
  # other hosts (e.g. the Steam CDN) are unaffected and always direct. Empty = connect to GitHub directly.
  github_proxy: ""
  # Standard HTTP(S)_PROXY, applies to ALL downloads (including non-GitHub), a fallback for users with only a generic egress proxy.
  http_proxy: ""
  timeout: 30s   # only bounds connection setup and waiting for response headers, not the transfer of large files
  retries: 3

# ArkApi offsets cache prefetch: BEFORE AsaApiLoader.exe starts, this program downloads, verifies and commits
# the cache ArkApi needs in the format it expects, so the loader just uses the local cache on startup.
# Why it is worth it: that download is made by ArkApi's own C++ code, which cannot read download.http_proxy above,
# has no resume support and runs under a 10-minute overall deadline -- a link too slow to finish in 10 minutes never finishes.
# Any prefetch failure silently falls back to "ArkApi downloads it itself"; it never breaks an instance that would otherwise start.
# See docs/ARKAPI_CACHE_PREFETCH_PLAN.md.
arkapi_cache:
  enabled: true                    # false = do not interfere at all
  # CDN prefix list, tried in order; empty = built-in default list (same order as ArkApi's default):
  #   https://cdn.pelayori.com/cache/
  #   https://cdn.shadowhunter.co.za/cache/
  #   https://cdn.shadowhunter-systems.co.za/cache/
  # The order MUST match ArkApi's default order, especially the first entry: the last_modified written into the
  # cache metadata has to come from the CDN ArkApi queries first, otherwise it considers the cache stale and re-downloads it all.
  urls: []
  keep_generations: 0              # extra generations kept in the source dir besides the current one (ArkApi cleans the mirror itself)
  max_size: 805306368              # 768 MiB, same limit as ArkApi's

# Linux only: Wine/Proton runtime (used to run the Windows ARK server exe on Linux).
# This whole section is ignored on Windows.
linux:
  # Runtime source: umu (default; the program downloads and manages umu-launcher + GE-Proton)
  #               | custom (bring your own PROTONPATH; the program only does read-only checks, no downloads)
  runtime: umu
  # Must be a concrete version, not "latest" -- resolving aliases via the GitHub API hits rate limits, see docs.
  umu_version: "1.4.4"
  proton_version: "GE-Proton10-34"
  # prefix mode: one prefix directory = one wineserver = one Wine session.
  #   shared       Default. All instances share one Wine prefix, saving disk. One prefix has only one
  #                wineserver, so instance **startups are serialized automatically** (the next one starts after
  #                the previous one initialized), and **only one ArkApi-enabled instance can run at a time**
  #                (a second one hangs until timeout).
  #   per-instance One umu-prefix-<instance name> per instance: fully isolated, startups can run in parallel,
  #                run as many ArkApi instances as you like. Cost: a full prefix per instance, and the first
  #                start takes about a minute longer to create it (GE-Proton and the Steam Linux Runtime
  #                are still shared globally, never downloaded twice).
  #   overlay      A shared read-only lower layer + one overlayfs writable layer per instance (umu-prefix-overlay/<instance name>/).
  #                Isolation like per-instance, disk and first-start cost close to shared. Needs root and kernel
  #                overlayfs; if mounting fails it degrades to "copy the lower layer" with a warning and the instance still starts.
  # Old directories do not disappear after switching back to shared; inspect and clean with "asa-server prefix status" / "asa-server prefix gc".
  prefix_mode: shared
  # shared mode only: once an instance has been initializing for this long, later instances stop
  # queueing behind it (the instance itself is not stopped). Must be at least 1m.
  launch_gate_timeout: 20m
  # Empty = {program dir}/umu-prefix
  # In per-instance mode this is a **prefix**, not the directory itself; the actual path is "<prefix_dir>-<instance name>"
  # In overlay mode it only decides where the lower layer lives; per-instance writable layers are always under {program dir}/umu-prefix-overlay/
  prefix_dir: ""
  # Which Python interpreter runs umu-launcher (a zipapp).
  #   empty    : auto-detect the system interpreter, scanning python3 / python3.10 ... python3.20 and picking the highest
  #              (does not touch the system default python3, and does not discover venv/pyenv)
  #   non-empty: use only this one, no auto-detection. May be:
  #              - a bare name, e.g.  python3.14                    (looked up in PATH)
  #              - an absolute path, e.g. /usr/bin/python3.14
  #              - a venv interpreter, e.g. /opt/asa-venv/bin/python
  #              - a pyenv version interpreter, e.g. ~/.pyenv/versions/3.14.0/bin/python
  #                (use the real versions/<x>/bin/python path, not ~/.pyenv/shims/python)
  #   Requires Python >= 3.10; with privilege dropping the interpreter must be readable/executable by the
  #   runtime user (do not put it under some user's HOME)
  umu_python_bin: ""
  # When false, umu/GE-Proton are never downloaded; missing components are only reported by
  # GET /api/system/preflight and never repaired automatically
  auto_download: true
  # Before umu initializes, download the Steam Linux Runtime archive with this program's downloader into umu's cache.
  # umu's own 150~190MB download uses its built-in urllib3: no retries and it cannot read
  # download.http_proxy above -- the most common timeout on first install. Set false to let umu download it itself (for troubleshooting).
  steamrt_prefetch: true
  gameid: "umu-default"

  # --- Prerequisites of ArkApi (AsaApiLoader.exe) on Linux ---
  # ArkApi requires the Microsoft Visual C++ Redistributable, while a Wine/GE-Proton prefix only has
  # Wine's own DLLs of the same names. true = install it into the shared prefix the first time the runtime is prepared
  # (about 24MB download), and write native,builtin DLL overrides the way winetricks does.
  # You can turn it off if you do not use ArkApi; ArkApi-enabled instances then only log an extra warning on start, not blocked.
  install_vcredist: true
  # Installer URL, empty = https://aka.ms/vs/17/release/vc_redist.x64.exe
  # Microsoft's final download URL contains the file's SHA256 in its path; it is extracted and verified after redirects, no need to fill it in.
  vcredist_url: ""
  # Only needed when vcredist_url points to a self-hosted mirror whose URL has no such hash (lowercase hex).
  vcredist_sha256: ""
  # Appended to the game process's WINEDLLOVERRIDES; empty = not set.
  # The VC++ overrides are already written into the prefix registry at install time, no need to repeat them here.
  # Troubleshooting escape hatch, e.g. force a DLL back to Wine's builtin:  "msvcp140=b"
  wine_dll_overrides: ""

  # --- Graphical display (prerequisite of ArkApi and the VC++ installer) ---
  # AsaApiLoader.exe creates a Win32 window; if Wine cannot reach an X server it silently exits with code 3.
  # Resolution order: named > self-managed > inherited > discovered, i.e.
  #   (1) display below  (2) this program's self-managed Xvfb  (3) the DISPLAY env var  (4) an X server already running on the system
  # Default is (2): the only display started, monitored and stopped by this program; it is not affected by desktop logouts
  # and does not pop game windows onto a user's desktop. Just install Xvfb:
  #   apt install xvfb / dnf install xorg-x11-server-Xvfb / pacman -S xorg-server-xvfb
  # (3)(4) are only used when (1)(2) are unavailable, and the log then says why.
  #
  # Empty display = nothing named. To use the host's existing X server instead (to see the game window while
  # debugging, or when Xvfb is unusable here), hard-code it, e.g.  ":0"  (the WSLg display is :0)
  display: ""
  # Path to the Xvfb server binary, empty = search PATH and common locations.
  # Note: this is Xvfb (the X.Org server), not Debian's xvfb-run script.
  xvfb_bin: ""
  # Screen spec of the self-managed Xvfb, empty = 1280x1024x24 (for troubleshooting, normally leave it)
  xvfb_screen: ""
  # Whether /tmp/.X11-unix may be remounted read-write when it is mounted read-only.
  # The X socket path is hard-coded in X itself, so on WSL/WSLg (mounted exactly like that) this is
  # the only way to use the self-managed Xvfb. Only acts when asa-server runs as root and the mount really is
  # read-only (only flips the rw flag, does not hide WSLg's own :0); logs when it acts and restores on exit.
  # false = never touch the host mount table; on WSL fall back to WSLg's :0
  allow_x11_remount: true

  # Game instances run as a dedicated non-root user (only when asa-server itself runs as root), see
  # docs/UMU_RUNTIME_USER_PLAN.md. The asa-server process stays root; only the umu/wine/game process tree is demoted.
  umu_runtime_user: "asa-umu-runtime"   # created with useradd -r if missing
  umu_runtime_uid: 0                     # non-zero = fixed uid (keeps ownership stable when moving BaseDir between machines)
  umu_runtime_gid: 0                     # non-zero = fixed gid
  # true = deliberately run game processes as root: no privilege dropping, all self-checks skipped.
  # This is the only bypass for "asa-server refuses to start when the privilege-drop environment is not satisfied".
  # Default false -- better the service fails to start than silently running an internet-facing game process as root.
  umu_run_as_root: false
  # Whether the startup self-check forks a demoted child to do a real write probe (always on at the instance start gate)
  umu_runtime_deep_probe: false
`
