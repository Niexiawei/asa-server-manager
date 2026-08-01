// Package rconx 封装对实例的 RCON 连接与命令执行。
//
// 独立成包是为了让 instance 与 realtime 共用同一份连接流程：
// 两边原本各抄了一遍存活预检、配置加载、密码校验和 Dial，重试策略已经开始漂移，
// github.com/gorcon/rcon 这个第三方依赖也因此同时出现在两个包里。
package rconx

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
	procpkg "asa-server/internal/process"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gorcon/rcon"
)

// 默认重试策略，与拆分前 instance.SendRCONCommand 的行为一致。
const (
	DefaultAttempts      = 3
	DefaultRetryInterval = 2 * time.Second
)

// 调用方需要区分的失败原因。
//
// 尤其是 ErrNotRunning：倒计时播报打在已经下线的实例上是常态，
// 调用方据此决定只记 WARN 还是当成真错误。
var (
	ErrNotRunning    = errors.New("server is not running")
	ErrPasswordEmpty = errors.New("RCON password is empty")
	ErrConnectFailed = errors.New("failed to connect to RCON server")
)

type options struct {
	attempts      int
	retryInterval time.Duration
}

type Option func(*options)

// WithAttempts 设置连接尝试次数（含首次）。1 = 不重试。
func WithAttempts(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.attempts = n
		}
	}
}

// WithRetryInterval 设置重试之间的等待时长。
func WithRetryInterval(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.retryInterval = d
		}
	}
}

// Execute 向实例发送一条 RCON 命令并返回响应。
//
// 内部完成：存活预检 → 加载实例配置 → 空密码校验 → 带重试的 Dial → Execute → Close。
// ctx 取消时中断重试等待并立即返回（拆分前用的是裸 time.Sleep，不可中断）。
func Execute(ctx context.Context, instanceName, command string, opts ...Option) (string, error) {
	o := options{attempts: DefaultAttempts, retryInterval: DefaultRetryInterval}
	for _, apply := range opts {
		apply(&o)
	}

	running, err := procpkg.IsServerRunning(instanceName)
	if err != nil || !running {
		return "", fmt.Errorf("instance %s: %w", instanceName, ErrNotRunning)
	}

	config, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		return "", err
	}

	if config.ServerAdminPassword == "" {
		return "", fmt.Errorf("instance %s: %w. Please set ServerAdminPassword in config",
			instanceName, ErrPasswordEmpty)
	}

	addr := fmt.Sprintf("localhost:%d", config.RCONPort)

	client, err := dial(ctx, addr, config.ServerAdminPassword, instanceName, &o)
	if err != nil {
		return "", err
	}
	defer client.Close()

	logger.GetLogger().Infof("Sending RCON command '%s' to %s", command, addr)
	response, err := client.Execute(command)
	if err != nil {
		return "", fmt.Errorf("RCON command execution failed: %w", err)
	}

	logger.GetLogger().Infof("RCON response: %s", response)
	return response, nil
}

// dial 按配置的次数尝试建立连接。
func dial(ctx context.Context, addr, password, instanceName string, o *options) (*rcon.Conn, error) {
	logger.GetLogger().Infof("Instance: %s Connecting to RCON server at %s...", instanceName, addr)

	var lastErr error
	for attempt := 1; attempt <= o.attempts; attempt++ {
		client, err := rcon.Dial(addr, password)
		if err == nil {
			logger.GetLogger().Info("Connected to RCON server")
			return client, nil
		}
		lastErr = err

		logger.GetLogger().Warnf("Attempt %d failed: %v", attempt, err)
		if attempt >= o.attempts {
			break
		}

		logger.GetLogger().Infof("   Retrying in %s...", o.retryInterval)
		select {
		case <-time.After(o.retryInterval):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	logger.GetLogger().Errorf("RCON connection to %s failed after %d attempts: %v",
		addr, o.attempts, lastErr)
	return nil, fmt.Errorf("%w at %s: %w", ErrConnectFailed, addr, lastErr)
}
