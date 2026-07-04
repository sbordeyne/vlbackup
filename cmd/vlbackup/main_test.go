package main

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// TestMainSmoke boots the server via main() on a loopback port and confirms it
// serves /healthz. main() blocks on ListenAndServe, so it runs in a goroutine
// that is intentionally left running when the test returns.
func TestMainSmoke(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"vlbackup"}
	t.Setenv("HOST", "127.0.0.1:18099")

	go main()

	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:18099/healthz")
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
		}
		return
	}
	t.Fatalf("server did not become ready: %v", lastErr)
}
