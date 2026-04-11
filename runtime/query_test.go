package runtime_test

import (
	"net/http/httptest"
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

func TestDecodeQueryParams_Basic(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?paging.page=2&paging.limit=20", nil)

	msg := &pb.NestedRequest{Title: "test"}
	if err := runtime.DecodeQueryParams(r, msg, "paging"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Paging == nil {
		t.Fatal("expected paging to be populated")
	}
	if msg.Paging.Page != 2 {
		t.Fatalf("expected page=2, got %d", msg.Paging.Page)
	}
	if msg.Paging.Limit != 20 {
		t.Fatalf("expected limit=20, got %d", msg.Paging.Limit)
	}
}

func TestDecodeQueryParams_UnknownFieldIgnored(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?paging.unknown=foo&paging.page=1", nil)

	msg := &pb.NestedRequest{}
	if err := runtime.DecodeQueryParams(r, msg, "paging"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Paging.Page != 1 {
		t.Fatalf("expected page=1, got %d", msg.Paging.Page)
	}
}

func TestDecodeQueryParams_InvalidTarget(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?page=1", nil)

	msg := &pb.NestedRequest{}
	if err := runtime.DecodeQueryParams(r, msg, "nonexistent"); err == nil {
		t.Fatal("expected error for invalid target field")
	}
}

func TestDecodeQueryParams_InvalidInt(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?paging.page=abc", nil)

	msg := &pb.NestedRequest{}
	if err := runtime.DecodeQueryParams(r, msg, "paging"); err == nil {
		t.Fatal("expected error for invalid int value")
	}
}
