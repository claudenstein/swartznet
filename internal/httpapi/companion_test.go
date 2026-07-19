package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

type fakeCompanion struct {
	pubStatus  CompanionPublisherStatus
	subStatus  []CompanionFollowStatus
	refreshErr error
	followErr  error
	lastFollow [32]byte
	lastLabel  string
}

func (f *fakeCompanion) PublisherStatus() CompanionPublisherStatus { return f.pubStatus }
func (f *fakeCompanion) RefreshNow() error                         { return f.refreshErr }
func (f *fakeCompanion) SubscriberStatus() []CompanionFollowStatus { return f.subStatus }
func (f *fakeCompanion) Follow(pk [32]byte, label string) error {
	f.lastFollow, f.lastLabel = pk, label
	return f.followErr
}
func (f *fakeCompanion) Unfollow(pk [32]byte) error { f.lastFollow = pk; return f.followErr }

func TestCompanionStatusRoute(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{
		pubStatus: CompanionPublisherStatus{PubKeyHex: strings.Repeat("ab", 32), PublishedCount: 3},
		subStatus: []CompanionFollowStatus{{PubKeyHex: strings.Repeat("cd", 32), TorrentsImported: 2}},
	}
	addr := startSearchServer(t, Options{Companion: fc})
	resp, err := http.Get("http://" + addr + "/companion")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var doc CompanionStatusResponse
	json.NewDecoder(resp.Body).Decode(&doc)
	if doc.Publisher.PublishedCount != 3 || len(doc.Subscriber) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
}

func TestCompanionStatusUnconfigured503(t *testing.T) {
	t.Parallel()
	addr := startSearchServer(t, Options{}) // no Companion
	resp, err := http.Get("http://" + addr + "/companion")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestCompanionRefreshThrottle429(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{refreshErr: errors.New("companion: refresh throttled")}
	addr := startSearchServer(t, Options{Companion: fc})
	req, _ := http.NewRequest("POST", "http://"+addr+"/companion/refresh", bytes.NewReader([]byte("{}")))
	req.Header.Set("Origin", "http://localhost:7654")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("throttled refresh = %d, want 429", resp.StatusCode)
	}
}

func TestCompanionFollowValidatesPubKey(t *testing.T) {
	t.Parallel()
	fc := &fakeCompanion{}
	addr := startSearchServer(t, Options{Companion: fc})
	post := func(body string) int {
		req, _ := http.NewRequest("POST", "http://"+addr+"/companion/follow", bytes.NewReader([]byte(body)))
		req.Header.Set("Origin", "http://localhost:7654")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(`{"pubkey":"tooshort"}`); code != http.StatusBadRequest {
		t.Errorf("short pubkey = %d, want 400", code)
	}
	if code := post(`{"pubkey":"` + strings.Repeat("zz", 32) + `"}`); code != http.StatusBadRequest {
		t.Errorf("non-hex pubkey = %d, want 400", code)
	}
	good := strings.Repeat("ab", 32)
	if code := post(`{"pubkey":"` + good + `","label":"seed"}`); code != http.StatusOK {
		t.Errorf("valid follow = %d, want 200", code)
	}
	if fc.lastLabel != "seed" {
		t.Errorf("label not threaded: %q", fc.lastLabel)
	}
}
