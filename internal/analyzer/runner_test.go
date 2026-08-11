package analyzer

import (
	"strings"
	"testing"
)

func TestLimitedBufferTruncatesWithoutShortWrite(t *testing.T) {
	buffer := newLimitedBuffer(5)
	input := []byte("123456789")
	written, err := buffer.Write(input)
	if err != nil || written != len(input) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if !buffer.Truncated() || !strings.Contains(buffer.String(), "12345") || !strings.Contains(buffer.String(), "truncated") {
		t.Fatalf("unexpected buffer: %q", buffer.String())
	}
}
