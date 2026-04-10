package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// fakeCompanion satisfies httpapi.CompanionController so the
// companion endpoints can be exercised without spinning up a
// real publisher / subscriber worker.
type fakeCompanion struct {
	mu          sync.Mutex
	pubStatus   httpapi.CompanionPublisherStatus
	subStatus   []httpapi.CompanionFollowStatus
	refreshErr  error
	follows     []followCall
	unfollows   []followCall
	followErr   error
	unfollowErr error
}

type followCall struct {
	pubkey [32]byte
	label  string
}

func (f *fakeCompanion) PublisherStatus() httpapi.CompanionPublisherStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pubStatus
}

func (f *fakeCompanion) RefreshNow() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshErr
}

func (f *fakeCompanion) SubscriberStatus() []httpapi.CompanionFollowStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subStatus
}

func (f *fakeCompanion) Follow(pubkey [32]byte, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.followErr != nil {
		return f.followErr
	}
	f.follows = append(f.follows, followCall{pubkey: pubkey, label: label})
	return nil
}

func (f *fakeCompanion) Unfollow(pubkey [32]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unfollowErr != nil {
		return f.unfollowErr
	}
	f.unfollows = append(f.unfollows, followCall{pubkey: pubkey})
	return nil
}

func newCompanionTestServer(t *testing.T, fc *fakeCompanion) (*httpapi.Server, string) {
	t.Helper()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), httpapi.Options{
		Companion: fc,
	})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s, "http://" + s.Addr()
}

func TestHTTPCompanionStatus(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{
		pubStatus: httpapi.CompanionPublisherStatus{
			PubKeyHex:      "abcd",
			LastInfoHash:   "1234",
			PublishedCount: 7,
		},
		subStatus: []httpapi.CompanionFollowStatus{
			{PubKeyHex: "ee", Label: "test", TorrentsImported: 3},
		},
	}
	_, base := newCompanionTestServer(t, fc)

	resp, err := http.Get(base + "/companion")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var out httpapi.CompanionStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Publisher.PubKeyHex != "abcd" {
		t.Errorf("publisher pubkey = %q, want abcd", out.Publisher.PubKeyHex)
	}
	if len(out.Subscriber) != 1 || out.Subscriber[0].PubKeyHex != "ee" {
		t.Errorf("subscriber rows = %+v", out.Subscriber)
	}
}

func TestHTTPCompanionStatusUnconfigured(t *testing.T) {
	t.Parallel()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), httpapi.Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, err := http.Get("http://" + s.Addr() + "/companion")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHTTPCompanionRefresh(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	resp, err := http.Post(base+"/companion/refresh", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("refresh status = %d, want 200", resp.StatusCode)
	}
}

func TestHTTPCompanionRefreshThrottled(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{refreshErr: errors.New("too soon")}
	_, base := newCompanionTestServer(t, fc)

	resp, err := http.Post(base+"/companion/refresh", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("refresh status = %d, want 429", resp.StatusCode)
	}
}

func TestHTTPCompanionFollow(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	body := `{"pubkey":"` + bytesHex64() + `","label":"test"}`
	resp, err := http.Post(base+"/companion/follow", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("follow status = %d body=%s", resp.StatusCode, buf)
	}
	if len(fc.follows) != 1 {
		t.Errorf("follow count = %d, want 1", len(fc.follows))
	}
	if fc.follows[0].label != "test" {
		t.Errorf("follow label = %q, want test", fc.follows[0].label)
	}
}

func TestHTTPCompanionFollowBadPubKey(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	body := `{"pubkey":"too-short","label":"x"}`
	resp, err := http.Post(base+"/companion/follow", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("follow status = %d, want 400", resp.StatusCode)
	}
	if len(fc.follows) != 0 {
		t.Errorf("follow recorded despite bad pubkey: %+v", fc.follows)
	}
}

func TestHTTPCompanionUnfollow(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	_, base := newCompanionTestServer(t, fc)

	body := `{"pubkey":"` + bytesHex64() + `"}`
	resp, err := http.Post(base+"/companion/unfollow", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("unfollow status = %d body=%s", resp.StatusCode, buf)
	}
	if len(fc.unfollows) != 1 {
		t.Errorf("unfollow count = %d, want 1", len(fc.unfollows))
	}
}

// bytesHex64 returns 64 zero hex chars — a syntactically valid
// pubkey for the validation pass. The fake companion does not
// care what the bytes are.
func bytesHex64() string {
	return "0000000000000000000000000000000000000000000000000000000000000000"
}
