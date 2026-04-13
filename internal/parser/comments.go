package parser

import (
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
)

// commentLookup resolves leading comments from a FileDescriptorProto's
// SourceCodeInfo. Proto descriptors encode source positions as a path of
// field-number-and-index pairs; for service methods that path is
// [6, serviceIndex, 2, methodIndex] where 6 is the FileDescriptorProto.service
// field number and 2 is the ServiceDescriptorProto.method field number.
type commentLookup struct {
	byPath map[string]string // joined path → leading comment (trimmed)
}

func buildLeadingComments(file *descriptorpb.FileDescriptorProto) commentLookup {
	out := commentLookup{byPath: map[string]string{}}
	info := file.GetSourceCodeInfo()
	if info == nil {
		return out
	}
	for _, loc := range info.Location {
		comment := loc.GetLeadingComments()
		if comment == "" {
			continue
		}
		out.byPath[joinPath(loc.Path)] = cleanComment(comment)
	}
	return out
}

// method returns the leading comment of file.service[svcIdx].method[methodIdx].
func (c commentLookup) method(svcIdx, methodIdx int) string {
	return c.byPath[joinPath([]int32{6, int32(svcIdx), 2, int32(methodIdx)})]
}

func joinPath(p []int32) string {
	var b strings.Builder
	for i, v := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(itoa(v))
	}
	return b.String()
}

func itoa(v int32) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// cleanComment strips the per-line leading space and asterisks that protoc
// preserves from `//` and `/* */` comments, and trims surrounding whitespace.
func cleanComment(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		lines[i] = line
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	return out
}
