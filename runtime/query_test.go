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

func TestDecodeQueryParams_BoolParam(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.active=true", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || !msg.Params.Active {
		t.Fatal("expected active=true")
	}
}

func TestDecodeQueryParams_BoolParam_Invalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.active=notabool", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid bool")
	}
}

func TestDecodeQueryParams_Int64Param(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.offset=9999999999", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Offset != 9999999999 {
		t.Fatalf("expected offset=9999999999, got %d", msg.Params.Offset)
	}
}

func TestDecodeQueryParams_FloatParam(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.score=3.14", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil {
		t.Fatal("expected params to be populated")
	}
	if msg.Params.Score < 3.13 || msg.Params.Score > 3.15 {
		t.Fatalf("expected score ~3.14, got %f", msg.Params.Score)
	}
}

func TestDecodeQueryParams_FloatParam_Invalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.score=notfloat", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid float")
	}
}

func TestDecodeQueryParams_EnumByName(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.status=STATUS_ACTIVE", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Status != pb.Status_STATUS_ACTIVE {
		t.Fatalf("expected STATUS_ACTIVE, got %v", msg.Params.GetStatus())
	}
}

func TestDecodeQueryParams_EnumByNumber(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.status=2", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Status != pb.Status_STATUS_INACTIVE {
		t.Fatalf("expected STATUS_INACTIVE (2), got %v", msg.Params.GetStatus())
	}
}

func TestDecodeQueryParams_EnumInvalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.status=BOGUS_VALUE", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid enum value")
	}
}

func TestDecodeQueryParams_StringParam(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.search=hello+world", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Search != "hello world" {
		t.Fatalf("expected search='hello world', got %q", msg.Params.GetSearch())
	}
}

func TestDecodeQueryParams_NonMessageTarget(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?id=1", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "id"); err == nil {
		t.Fatal("expected error for non-message target field")
	}
}

func TestDecodeQueryParams_ShortKeyWithoutPrefix(t *testing.T) {
	// Keys without the target prefix should be looked up directly.
	r := httptest.NewRequest("GET", "/items?search=direct", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Search != "direct" {
		t.Fatalf("expected search='direct', got %q", msg.Params.GetSearch())
	}
}

func TestDecodeQueryParams_Uint32Param(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.page_size=50", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.PageSize != 50 {
		t.Fatalf("expected page_size=50, got %d", msg.Params.GetPageSize())
	}
}

func TestDecodeQueryParams_Uint32Param_Invalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.page_size=-1", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid uint32")
	}
}

func TestDecodeQueryParams_Uint64Param(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.cursor=18446744073709551610", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Cursor != 18446744073709551610 {
		t.Fatalf("expected cursor=18446744073709551610, got %d", msg.Params.GetCursor())
	}
}

func TestDecodeQueryParams_Uint64Param_Invalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.cursor=notanumber", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid uint64")
	}
}

func TestDecodeQueryParams_DoubleParam(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.weight=2.718281828", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Weight < 2.71 || msg.Params.Weight > 2.72 {
		t.Fatalf("expected weight ~2.718, got %f", msg.Params.GetWeight())
	}
}

func TestDecodeQueryParams_DoubleParam_Invalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.weight=notadouble", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid double")
	}
}

func TestDecodeQueryParams_Int32Param(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.page=5", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Params == nil || msg.Params.Page != 5 {
		t.Fatalf("expected page=5, got %d", msg.Params.GetPage())
	}
}

func TestDecodeQueryParams_Int32Param_Invalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.page=abc", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid int32")
	}
}

func TestDecodeQueryParams_Int64Param_Invalid(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?params.offset=notint", nil)

	msg := &pb.QueryRequest{Id: "1"}
	if err := runtime.DecodeQueryParams(r, msg, "params"); err == nil {
		t.Fatal("expected error for invalid int64")
	}
}
