package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestHTTPCompanionRefreshUnconfigured covers the "no companion
// controller wired" 503 path of POST /companion/refresh. The
// happy and throttled paths are covered in companion_test.go.
func TestHTTPCompanionRefreshUnconfigured(t *testing.T) {
	t.Parallel()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), httpapi.Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, err := http.Post("http://"+s.Addr()+"/companion/refresh", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHTTPCompanionFollowUnconfigured(t *testing.T) {
	t.Parallel()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), httpapi.Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, err := http.Post("http://"+s.Addr()+"/companion/follow", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHTTPCompanionUnfollowUnconfigured(t *testing.T) {
	t.Parallel()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), httpapi.Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, err := http.Post("http://"+s.Addr()+"/companion/unfollow", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHTTPCompanionFollowBadJSON(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	resp, err := http.Post(base+"/companion/follow", "application/json", strings.NewReader(`{nope`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPCompanionUnfollowBadJSON(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	resp, err := http.Post(base+"/companion/unfollow", "application/json", strings.NewReader(`{nope`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPCompanionUnfollowBadPubKey(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	body := `{"pubkey":"only-32-chars-long-not-64-zzzzz"}`
	resp, err := http.Post(base+"/companion/unfollow", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if len(fc.unfollows) != 0 {
		t.Errorf("unfollow recorded despite bad pubkey: %+v", fc.unfollows)
	}
}

// TestHTTPCompanionFollowParseHexError exercises the
// parseFollowPubKey error branch where length is exactly 64 but
// the bytes are not all valid hex digits — hex.DecodeString fails.
func TestHTTPCompanionFollowParseHexError(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	// 64 chars exactly, but 'z' is not a hex digit.
	pubkey := strings.Repeat("z", 64)
	body := `{"pubkey":"` + pubkey + `","label":"x"}`
	resp, err := http.Post(base+"/companion/follow", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestHTTPCompanionFollowControllerError covers the path where the
// controller returns an error from Follow (e.g. nil-subscriber or
// disk-write failure).
func TestHTTPCompanionFollowControllerError(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{followErr: errors.New("subscriber not configured")}
	_, base := newCompanionTestServer(t, fc)

	body := `{"pubkey":"` + bytesHex64() + `","label":"x"}`
	resp, err := http.Post(base+"/companion/follow", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestHTTPCompanionUnfollowControllerError(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{unfollowErr: errors.New("subscriber not configured")}
	_, base := newCompanionTestServer(t, fc)

	body := `{"pubkey":"` + bytesHex64() + `"}`
	resp, err := http.Post(base+"/companion/unfollow", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}
