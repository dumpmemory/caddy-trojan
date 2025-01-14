package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/certmagic"
	"go.uber.org/zap"

	"github.com/imgk/caddy-trojan/trojan"
	"github.com/imgk/caddy-trojan/utils"
)

func init() {
	caddy.RegisterModule(CaddyUpstream{})
	caddy.RegisterModule(MemoryUpstream{})
}

// Upstream is ...
type Upstream interface {
	// Add is ...
	Add(string) error
	// AddKey is ...
	AddKey(string) error
	// Del is ...
	Del(string) error
	// DelKey is ...
	DelKey(string) error
	// Range is ...
	Range(func(string, int64, int64))
	// Validate is ...
	Validate(string) bool
	// Consume is ...
	Consume(string, int64, int64) error
}

// MemoryUpstream is ...
type MemoryUpstream struct {
	mu sync.RWMutex
	mm map[string]Traffic
}

// CaddyModule is ...
func (MemoryUpstream) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "trojan.upstreams.memory",
		New: func() caddy.Module { return new(MemoryUpstream) },
	}
}

// AddKey is ...
func (u *MemoryUpstream) AddKey(k string) error {
	key := base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	u.mu.Lock()
	u.mm[key] = Traffic{
		Up:   0,
		Down: 0,
	}
	u.mu.Unlock()
	return nil
}

// Add is ...
func (u *MemoryUpstream) Add(s string) error {
	b := [trojan.HeaderLen]byte{}
	trojan.GenKey(s, b[:])
	return u.AddKey(utils.ByteSliceToString(b[:]))
}

// DelKey is ...
func (u *MemoryUpstream) DelKey(k string) error {
	key := base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	u.mu.Lock()
	delete(u.mm, key)
	u.mu.Unlock()
	return nil
}

// Del is ...
func (u *MemoryUpstream) Del(s string) error {
	b := [trojan.HeaderLen]byte{}
	trojan.GenKey(s, b[:])
	return u.DelKey(utils.ByteSliceToString(b[:]))
}

// Range is ...
func (u *MemoryUpstream) Range(fn func(string, int64, int64)) {
	u.mu.RLock()
	for k, v := range u.mm {
		fn(k, v.Up, v.Down)
	}
	u.mu.RUnlock()
}

// Validate is ...
func (u *MemoryUpstream) Validate(k string) bool {
	// base64.StdEncoding.EncodeToString(hex.Encode(sha256.Sum224([]byte("Test1234"))))
	const AuthLen = 76
	if len(k) != AuthLen {
		k = base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	}
	u.mu.RLock()
	_, ok := u.mm[k]
	u.mu.RUnlock()
	return ok
}

// Consume is ...
func (u *MemoryUpstream) Consume(k string, nr, nw int64) error {
	// base64.StdEncoding.EncodeToString(hex.Encode(sha256.Sum224([]byte("Test1234"))))
	const AuthLen = 76
	if len(k) != AuthLen {
		k = base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	}
	u.mu.Lock()
	traffic := u.mm[k]
	traffic.Up += nr
	traffic.Down += nw
	u.mm[k] = traffic
	u.mu.Unlock()
	return nil
}

// CaddyUpstream is ...
type CaddyUpstream struct {
	// Prefix is ...
	Prefix string `json:"-,omitempty"`
	// Storage is ...
	Storage certmagic.Storage `json:"-,omitempty"`
	// Logger is ...
	Logger *zap.Logger `json:"-,omitempty"`
}

// CaddyModule is ...
func (CaddyUpstream) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "trojan.upstreams.caddy",
		New: func() caddy.Module { return new(CaddyUpstream) },
	}
}

// Provision is ...
func (u *CaddyUpstream) Provision(ctx caddy.Context) error {
	u.Prefix = "trojan/"
	u.Storage = ctx.Storage()
	u.Logger = ctx.Logger(u)
	return nil
}

// AddKey is ...
func (u *CaddyUpstream) AddKey(k string) error {
	key := u.Prefix + base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	if u.Storage.Exists(context.Background(), key) {
		return nil
	}
	traffic := Traffic{
		Up:   0,
		Down: 0,
	}
	b, err := json.Marshal(&traffic)
	if err != nil {
		return err
	}
	return u.Storage.Store(context.Background(), key, b)
}

// Add is ...
func (u *CaddyUpstream) Add(s string) error {
	b := [trojan.HeaderLen]byte{}
	trojan.GenKey(s, b[:])
	return u.AddKey(utils.ByteSliceToString(b[:]))
}

// DelKey is ...
func (u *CaddyUpstream) DelKey(k string) error {
	key := u.Prefix + base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	if !u.Storage.Exists(context.Background(), key) {
		return nil
	}
	return u.Storage.Delete(context.Background(), key)
}

// Del is ...
func (u *CaddyUpstream) Del(s string) error {
	b := [trojan.HeaderLen]byte{}
	trojan.GenKey(s, b[:])
	return u.DelKey(utils.ByteSliceToString(b[:]))
}

// Range is ...
func (u *CaddyUpstream) Range(fn func(k string, up, down int64)) {
	// base64.StdEncoding.EncodeToString(hex.Encode(sha256.Sum224([]byte("Test1234"))))
	const AuthLen = 76

	keys, err := u.Storage.List(context.Background(), u.Prefix, false)
	if err != nil {
		return
	}

	traffic := Traffic{}
	for _, k := range keys {
		b, err := u.Storage.Load(context.Background(), k)
		if err != nil {
			u.Logger.Error(fmt.Sprintf("load user error: %v", err))
			continue
		}
		if err := json.Unmarshal(b, &traffic); err != nil {
			u.Logger.Error(fmt.Sprintf("load user error: %v", err))
			continue
		}
		fn(strings.TrimPrefix(k, u.Prefix), traffic.Up, traffic.Down)
	}

	return
}

// Validate is ...
func (u *CaddyUpstream) Validate(k string) bool {
	// base64.StdEncoding.EncodeToString(hex.Encode(sha256.Sum224([]byte("Test1234"))))
	const AuthLen = 76
	if len(k) == AuthLen {
		k = u.Prefix + k
	} else {
		k = u.Prefix + base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	}
	return u.Storage.Exists(context.Background(), k)
}

// Consume is ...
func (u *CaddyUpstream) Consume(k string, nr, nw int64) error {
	// base64.StdEncoding.EncodeToString(hex.Encode(sha256.Sum224([]byte("Test1234"))))
	const AuthLen = 76
	if len(k) == AuthLen {
		k = u.Prefix + k
	} else {
		k = u.Prefix + base64.StdEncoding.EncodeToString(utils.StringToByteSlice(k))
	}

	u.Storage.Lock(context.Background(), k)
	defer u.Storage.Unlock(context.Background(), k)

	b, err := u.Storage.Load(context.Background(), k)
	if err != nil {
		return err
	}

	traffic := Traffic{}
	if err := json.Unmarshal(b, &traffic); err != nil {
		return err
	}

	traffic.Up += nr
	traffic.Down += nw

	b, err = json.Marshal(&traffic)
	if err != nil {
		return err
	}

	return u.Storage.Store(context.Background(), k, b)
}

var (
	_ Upstream = (*CaddyUpstream)(nil)
	_ Upstream = (*MemoryUpstream)(nil)
)
