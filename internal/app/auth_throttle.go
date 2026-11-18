package app

import (
	"sync"
	"time"
)

const (
	authThrottleFreeAttempts = 5
	authThrottleBaseLockout  = 30 * time.Second
	authThrottleMaxLockout   = 30 * time.Minute
	authThrottleIdleReset    = 30 * time.Minute
	authThrottleGlobalWindow = time.Minute
	authThrottleGlobalMax    = 50
	authThrottleGlobalCool   = time.Minute
)

type authThrottleBucket struct {
	failures  int
	lockUntil time.Time
	lastSeen  time.Time
}

// authThrottle 限制管理员登录与改密接口的失败频率，规则对齐 Diana WebUI：
// 同一客户端 IP 前 5 次失败允许手滑重试；从第 6 次起按 30s * 2^(n-6) 指数退避，
// 最长封锁 30 分钟；同时对全局失败速率做兜底限流，防止分布式撞库打满 PBKDF2。
type authThrottle struct {
	mu             sync.Mutex
	buckets        map[string]*authThrottleBucket
	globalFailures []time.Time
	globalUntil    time.Time
	now            func() time.Time
}

func newAuthThrottle() *authThrottle {
	return &authThrottle{
		buckets: make(map[string]*authThrottleBucket),
		now:     time.Now,
	}
}

func (t *authThrottle) check(ip string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.pruneLocked(now)
	if now.Before(t.globalUntil) {
		return t.globalUntil.Sub(now)
	}
	if b, ok := t.buckets[ip]; ok && now.Before(b.lockUntil) {
		return b.lockUntil.Sub(now)
	}
	return 0
}

func (t *authThrottle) recordFailure(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.pruneLocked(now)

	b := t.buckets[ip]
	if b == nil {
		b = &authThrottleBucket{}
		t.buckets[ip] = b
	}
	b.failures++
	b.lastSeen = now
	if b.failures > authThrottleFreeAttempts {
		shift := b.failures - authThrottleFreeAttempts - 1
		if shift > 10 {
			shift = 10
		}
		backoff := authThrottleBaseLockout << shift
		if backoff > authThrottleMaxLockout {
			backoff = authThrottleMaxLockout
		}
		b.lockUntil = now.Add(backoff)
	}

	t.globalFailures = append(t.globalFailures, now)
	if len(t.globalFailures) >= authThrottleGlobalMax {
		t.globalUntil = now.Add(authThrottleGlobalCool)
		t.globalFailures = nil
	}
}

func (t *authThrottle) recordSuccess(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.buckets, ip)
}

func (t *authThrottle) pruneLocked(now time.Time) {
	for ip, b := range t.buckets {
		anchor := b.lastSeen
		if b.lockUntil.After(anchor) {
			anchor = b.lockUntil
		}
		if now.Sub(anchor) > authThrottleIdleReset {
			delete(t.buckets, ip)
		}
	}
	cutoff := now.Add(-authThrottleGlobalWindow)
	idx := 0
	for idx < len(t.globalFailures) && t.globalFailures[idx].Before(cutoff) {
		idx++
	}
	if idx > 0 {
		t.globalFailures = append([]time.Time(nil), t.globalFailures[idx:]...)
	}
}
