package execx

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func encodeUTF16LE(text string) []byte {
	units := utf16.Encode([]rune(text))
	buffer := make([]byte, 2+2*len(units))
	buffer[0] = 0xFF
	buffer[1] = 0xFE
	for index, unit := range units {
		buffer[2+index*2] = byte(unit)
		buffer[3+index*2] = byte(unit >> 8)
	}
	return buffer
}

func TestDecodeTextUTF8(t *testing.T) {
	raw := []byte("NAME      STATE           VERSION\n* Debian  Stopped         2\n")
	if got := DecodeText(raw); got != string(raw) {
		t.Fatalf("unexpected decode: %q", got)
	}
}

func TestDecodeTextUTF8BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("wslc 3.0.3.0")...)
	if got := DecodeText(raw); got != "wslc 3.0.3.0" {
		t.Fatalf("unexpected decode: %q", got)
	}
}

func TestDecodeTextUTF16LEWithBOM(t *testing.T) {
	raw := encodeUTF16LE("NAME STATE\n* Debian Stopped 2\n")
	want := "NAME STATE\n* Debian Stopped 2\n"
	if got := DecodeText(raw); got != want {
		t.Fatalf("unexpected decode: %q", got)
	}
}

func TestDecodeTextUTF16LEWithoutBOM(t *testing.T) {
	raw := encodeUTF16LE("默认分发: Debian\n")
	if got := DecodeText(raw[2:]); got != "默认分发: Debian\n" {
		t.Fatalf("unexpected decode: %q", got)
	}
}

func TestDecodeTextChunkedSurrogatePair(t *testing.T) {
	raw := encodeUTF16LE("a🚀b")
	decoder := &chunkDecoder{}
	var builder strings.Builder
	// Split so that the BOM, the high surrogate and the low surrogate all land
	// in different chunks.
	for _, chunk := range [][]byte{raw[:1], raw[1:3], raw[3:6], raw[6:]} {
		builder.WriteString(decoder.Decode(chunk))
	}
	builder.WriteString(decoder.Flush())
	if got := builder.String(); got != "a🚀b" {
		t.Fatalf("unexpected decode: %q", got)
	}
}

func TestLineAssemblerUTF16Lines(t *testing.T) {
	assembler := newLineAssembler()
	raw := encodeUTF16LE("line one\nline two\n尾行")
	var lines []string
	// Feed one byte at a time to exercise partial unit buffering.
	for index := 0; index < len(raw); index++ {
		lines = append(lines, assembler.Write(raw[index:index+1])...)
	}
	lines = append(lines, assembler.Flush()...)
	want := []string{"line one\n", "line two\n", "尾行"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d lines, got %d: %#v", len(want), len(lines), lines)
	}
	for index := range want {
		if lines[index] != want[index] {
			t.Fatalf("line %d: got %q want %q", index, lines[index], want[index])
		}
	}
}

func TestLimitedBufferTruncates(t *testing.T) {
	buffer := newLimitedBuffer(5)
	buffer.WriteString("abcdefg")
	if !buffer.truncated {
		t.Fatal("expected truncated flag")
	}
	if got := buffer.String(); got != "abcde" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestDisplayCommandQuotes(t *testing.T) {
	got := displayCommand(ToolWSL, []string{"-d", "Debian", "-e", "sh", "-lc", "uname -r && id -u"})
	want := `wsl -d Debian -e sh -lc 'uname -r && id -u'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
