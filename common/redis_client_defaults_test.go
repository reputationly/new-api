package common

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestRedisClientDefaultsC1EmptyOptions(t *testing.T) {
	opt := &redis.Options{}
	applyRedisClientDefaults(opt)

	if opt.DialTimeout != time.Second {
		t.Fatalf("DialTimeout = %s, want 1s", opt.DialTimeout)
	}
	if opt.ReadTimeout != 500*time.Millisecond {
		t.Fatalf("ReadTimeout = %s, want 500ms", opt.ReadTimeout)
	}
	if opt.WriteTimeout != 500*time.Millisecond {
		t.Fatalf("WriteTimeout = %s, want 500ms", opt.WriteTimeout)
	}
	if opt.PoolTimeout != time.Second {
		t.Fatalf("PoolTimeout = %s, want 1s", opt.PoolTimeout)
	}
	if opt.MaxRetries != -1 {
		t.Fatalf("MaxRetries = %d, want -1", opt.MaxRetries)
	}
}

func TestRedisClientDefaultsC2ExplicitOptionsPreserved(t *testing.T) {
	opt, err := redis.ParseURL("redis://127.0.0.1:6379/0?dial_timeout=3&read_timeout=2s&write_timeout=1s&pool_timeout=4s&max_retries=2")
	if err != nil {
		t.Fatalf("ParseURL error: %v", err)
	}
	applyRedisClientDefaults(opt)

	if opt.DialTimeout != 3*time.Second {
		t.Fatalf("DialTimeout = %s, want 3s", opt.DialTimeout)
	}
	if opt.ReadTimeout != 2*time.Second {
		t.Fatalf("ReadTimeout = %s, want 2s", opt.ReadTimeout)
	}
	if opt.WriteTimeout != time.Second {
		t.Fatalf("WriteTimeout = %s, want 1s", opt.WriteTimeout)
	}
	if opt.PoolTimeout != 4*time.Second {
		t.Fatalf("PoolTimeout = %s, want 4s", opt.PoolTimeout)
	}
	if opt.MaxRetries != 2 {
		t.Fatalf("MaxRetries = %d, want 2", opt.MaxRetries)
	}
}

func TestRedisClientDefaultsC3BlackholeReadTimesOutWithoutRetry(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				<-done
				_ = conn.Close()
			}(conn)
		}
	}()
	t.Cleanup(func() {
		close(done)
		_ = ln.Close()
	})

	opt := &redis.Options{Addr: ln.Addr().String()}
	applyRedisClientDefaults(opt)
	client := redis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })

	start := time.Now()
	_, err = client.Get(context.Background(), "key").Result()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Get succeeded against a blackhole listener, want error")
	}
	if elapsed < 400*time.Millisecond {
		t.Fatalf("Get returned too early after %s, want >= 400ms", elapsed)
	}
	if elapsed >= time.Second {
		t.Fatalf("Get took %s, want < 1s", elapsed)
	}
}

func TestRedisClientDefaultsC4ClosedPortFailsFast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	opt := &redis.Options{Addr: addr}
	applyRedisClientDefaults(opt)
	client := redis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })

	start := time.Now()
	_, err = client.Get(context.Background(), "key").Result()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Get succeeded against a closed port, want error")
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("Get took %s, want < 500ms", elapsed)
	}
}
