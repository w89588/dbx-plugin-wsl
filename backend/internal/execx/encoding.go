package execx

import (
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type textEncoding int

const (
	encodingUnknown textEncoding = iota
	encodingUTF8
	encodingUTF16LE
)

// DecodeText converts captured process output to UTF-8. wsl.exe is called with
// WSL_UTF8=1, but older builds still emit UTF-16LE; Detect a leading FF FE BOM
// or a high ratio of NUL bytes and decode accordingly (contract section 6.2).
func DecodeText(raw []byte) string {
	decoder := &chunkDecoder{}
	return decoder.Decode(raw) + decoder.Flush()
}

// chunkDecoder incrementally converts raw process output into UTF-8. It keeps
// undecided header bytes and unfinished multi-byte sequences between chunks so
// that UTF-16LE output and split UTF-8 runes survive pipe reads.
type chunkDecoder struct {
	encoding textEncoding
	// header holds the first bytes until the encoding can be decided.
	header []byte
	// pendingUTF8 holds a trailing incomplete UTF-8 sequence.
	pendingUTF8 []byte
	// pendingUTF16 holds a trailing odd byte and a pending high surrogate.
	pendingUTF16 []byte
	pendingUnits []uint16
}

// Decode converts one chunk to UTF-8.
func (d *chunkDecoder) Decode(chunk []byte) string {
	if len(chunk) == 0 {
		return ""
	}
	if d.encoding == encodingUnknown {
		d.header = append(d.header, chunk...)
		encoding, consumed := detectEncoding(d.header)
		if encoding == encodingUnknown {
			return ""
		}
		d.encoding = encoding
		chunk = d.header[consumed:]
		d.header = nil
		if len(chunk) == 0 {
			return ""
		}
	}
	switch d.encoding {
	case encodingUTF16LE:
		return d.decodeUTF16(chunk)
	default:
		return d.decodeUTF8(chunk)
	}
}

// Flush returns any trailing decoded text.
func (d *chunkDecoder) Flush() string {
	var builder strings.Builder
	if d.encoding == encodingUnknown {
		// Too short to detect: treat as UTF-8.
		builder.WriteString(d.decodeUTF8(d.header))
		d.header = nil
		return builder.String()
	}
	switch d.encoding {
	case encodingUTF16LE:
		if len(d.pendingUTF16) > 0 {
			d.pendingUnits = append(d.pendingUnits, uint16(d.pendingUTF16[0]))
			d.pendingUTF16 = nil
		}
		builder.WriteString(encodeUnits(d.pendingUnits))
		d.pendingUnits = nil
	default:
		if len(d.pendingUTF8) > 0 {
			builder.Write(d.pendingUTF8)
			d.pendingUTF8 = nil
		}
	}
	return builder.String()
}

func detectEncoding(header []byte) (textEncoding, int) {
	if len(header) >= 2 && header[0] == 0xFF && header[1] == 0xFE {
		return encodingUTF16LE, 2
	}
	if len(header) >= 2 && header[0] == 0xFE && header[1] == 0xFF {
		// UTF-16BE is not produced by WSL, but decode it as LE anyway after
		// stripping the BOM would be wrong; treat as UTF-16LE without BOM.
		return encodingUTF16LE, 2
	}
	if len(header) >= 3 && header[0] == 0xEF && header[1] == 0xBB && header[2] == 0xBF {
		return encodingUTF8, 3
	}
	if len(header) >= 2 && header[0] == 0xEF && header[1] == 0xBB {
		// Possible partial UTF-8 BOM, keep waiting.
		return encodingUnknown, 0
	}
	sample := header
	if len(sample) > 512 {
		sample = sample[:512]
	}
	if len(sample) < 2 {
		return encodingUnknown, 0
	}
	zeros := 0
	for _, b := range sample {
		if b == 0 {
			zeros++
		}
	}
	if zeros >= 2 && zeros*4 >= len(sample) {
		return encodingUTF16LE, 0
	}
	return encodingUTF8, 0
}

func (d *chunkDecoder) decodeUTF8(chunk []byte) string {
	chunk = bytes.TrimPrefix(chunk, []byte{0xEF, 0xBB, 0xBF})
	if len(d.pendingUTF8) > 0 {
		chunk = append(append([]byte(nil), d.pendingUTF8...), chunk...)
		d.pendingUTF8 = nil
	}
	if len(chunk) == 0 {
		return ""
	}
	// Hold back a trailing incomplete rune.
	for hold := 1; hold <= 3 && hold < len(chunk); hold++ {
		start := len(chunk) - hold
		if utf8.RuneStart(chunk[start]) {
			if !utf8.FullRune(chunk[start:]) {
				d.pendingUTF8 = append([]byte(nil), chunk[start:]...)
				chunk = chunk[:start]
			}
			break
		}
	}
	return string(chunk)
}

func (d *chunkDecoder) decodeUTF16(chunk []byte) string {
	if len(d.pendingUTF16) > 0 {
		chunk = append(append([]byte(nil), d.pendingUTF16...), chunk...)
		d.pendingUTF16 = nil
	}
	if len(chunk) == 0 {
		return ""
	}
	if len(chunk)%2 == 1 {
		d.pendingUTF16 = []byte{chunk[len(chunk)-1]}
		chunk = chunk[:len(chunk)-1]
	}
	for index := 0; index+1 < len(chunk); index += 2 {
		d.pendingUnits = append(d.pendingUnits, uint16(chunk[index])|uint16(chunk[index+1])<<8)
	}
	// Leave a trailing high surrogate buffered for the next chunk.
	decodable := len(d.pendingUnits)
	if decodable > 0 {
		last := d.pendingUnits[decodable-1]
		if last >= 0xD800 && last <= 0xDBFF {
			decodable--
		}
	}
	text := encodeUnits(d.pendingUnits[:decodable])
	if decodable != len(d.pendingUnits) {
		d.pendingUnits = append([]uint16(nil), d.pendingUnits[decodable:]...)
	} else {
		d.pendingUnits = nil
	}
	return text
}

// encodeUnits decodes UTF-16 code units to UTF-8.
func encodeUnits(units []uint16) string {
	if len(units) == 0 {
		return ""
	}
	var builder strings.Builder
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if unit >= 0xD800 && unit <= 0xDBFF {
			if index+1 < len(units) {
				r := utf16.DecodeRune(rune(unit), rune(units[index+1]))
				builder.WriteRune(r)
				index++
				continue
			}
			builder.WriteRune(utf8.RuneError)
			continue
		}
		if unit >= 0xDC00 && unit <= 0xDFFF {
			builder.WriteRune(utf8.RuneError)
			continue
		}
		builder.WriteRune(rune(unit))
	}
	return builder.String()
}

// lineAssembler splits decoded text into complete lines. Line text keeps its
// trailing "\n" (contract section 3.2: job/output.text is line based).
type lineAssembler struct {
	decoder *chunkDecoder
	pending string
}

func newLineAssembler() *lineAssembler {
	return &lineAssembler{decoder: &chunkDecoder{}}
}

// Write decodes a raw chunk and returns complete lines.
func (a *lineAssembler) Write(chunk []byte) []string {
	text := a.decoder.Decode(chunk)
	if text == "" {
		return nil
	}
	a.pending += text
	return a.takeLines()
}

// Flush returns the trailing text (without newline) as a final line.
func (a *lineAssembler) Flush() []string {
	tail := a.decoder.Flush()
	if tail != "" {
		a.pending += tail
	}
	lines := a.takeLines()
	if a.pending != "" {
		lines = append(lines, a.pending)
		a.pending = ""
	}
	return lines
}

func (a *lineAssembler) takeLines() []string {
	var lines []string
	for {
		index := strings.IndexByte(a.pending, '\n')
		if index < 0 {
			break
		}
		lines = append(lines, a.pending[:index+1])
		a.pending = a.pending[index+1:]
	}
	return lines
}
