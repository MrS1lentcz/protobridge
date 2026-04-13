package parser

import (
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCleanComment(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{" Hello\n", "Hello"},
		{" * line one\n * line two\n", "line one\nline two"},
		{"\n\n   first\n   second\n\n", "first\nsecond"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := cleanComment(tc.in); got != tc.want {
			t.Errorf("cleanComment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCommentLookup_NoSourceCodeInfo(t *testing.T) {
	c := buildLeadingComments(&descriptorpb.FileDescriptorProto{})
	if got := c.method(0, 0); got != "" {
		t.Errorf("expected empty for missing SourceCodeInfo, got %q", got)
	}
}

func TestCommentLookup_MethodComment(t *testing.T) {
	file := &descriptorpb.FileDescriptorProto{
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{
			Location: []*descriptorpb.SourceCodeInfo_Location{
				{
					// service[0].method[1] leading comment.
					Path:             []int32{6, 0, 2, 1},
					LeadingComments:  sp(" Creates a thing.\n Returns the new ID."),
				},
			},
		},
	}
	c := buildLeadingComments(file)
	if got := c.method(0, 1); got != "Creates a thing.\nReturns the new ID." {
		t.Errorf("got %q", got)
	}
	if got := c.method(0, 0); got != "" {
		t.Errorf("expected empty for unannotated path, got %q", got)
	}
}

func TestJoinPath(t *testing.T) {
	if got := joinPath([]int32{6, 0, 2, 1}); got != "6.0.2.1" {
		t.Errorf("got %q", got)
	}
	if got := joinPath(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestItoa(t *testing.T) {
	cases := []struct {
		in   int32
		want string
	}{
		{0, "0"}, {1, "1"}, {12345, "12345"}, {-7, "-7"},
	}
	for _, tc := range cases {
		if got := itoa(tc.in); got != tc.want {
			t.Errorf("itoa(%d) = %q", tc.in, got)
		}
	}
}
