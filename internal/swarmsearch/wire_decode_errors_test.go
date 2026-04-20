package swarmsearch_test

import (
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

func TestDecodeResultRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := swarmsearch.DecodeResult([]byte("not bencode")); err == nil {
		t.Error("DecodeResult on non-bencode should error")
	}
}

// TestDecodeResultRejectsWrongMsgType: a syntactically-valid
// payload whose msg_type is not MsgTypeResult is rejected.
func TestDecodeResultRejectsWrongMsgType(t *testing.T) {
	t.Parallel()
	q, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 1, Q: "x", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = swarmsearch.DecodeResult(q)
	if err == nil {
		t.Fatal("DecodeResult on a query payload should error")
	}
	if !strings.Contains(err.Error(), "not a result") {
		t.Errorf("error = %q, want it to mention 'not a result'", err)
	}
}

func TestDecodeRejectRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := swarmsearch.DecodeReject([]byte("not bencode")); err == nil {
		t.Error("DecodeReject on non-bencode should error")
	}
}

// TestDecodeRejectRejectsWrongMsgType: same wrong-msg_type guard
// applied to the reject decoder.
func TestDecodeRejectRejectsWrongMsgType(t *testing.T) {
	t.Parallel()
	q, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 1, Q: "x", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = swarmsearch.DecodeReject(q)
	if err == nil {
		t.Fatal("DecodeReject on a query payload should error")
	}
	if !strings.Contains(err.Error(), "not a reject") {
		t.Errorf("error = %q, want it to mention 'not a reject'", err)
	}
}
