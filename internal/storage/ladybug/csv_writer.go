package ladybug

import (
	"bufio"
	"os"
	"runtime"
	"strings"
)

// LadybugCSVWriter provides high-performance CSV row writing with platform-aware escaping.
// It reuses an internal strings.Builder across rows to minimize heap allocations,
// uses strings.Replacer.WriteString for single-pass streaming escape,
// and buffers output to reduce syscall frequency.
type LadybugCSVWriter struct {
	bufferedWriter *bufio.Writer
	builder        strings.Builder
	replacer       *strings.Replacer
}

// NewLadybugCSVWriter creates a CSV writer for the given file.
// Escape strategy is determined by platform:
//   - Linux/macOS: backslash escape (\" and \\)
//   - Windows: doubled quote ("")
func NewLadybugCSVWriter(file *os.File) *LadybugCSVWriter {
	var replacer *strings.Replacer
	if runtime.GOOS != "windows" {
		replacer = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	} else {
		replacer = strings.NewReplacer(`"`, `""`)
	}
	return &LadybugCSVWriter{
		bufferedWriter: bufio.NewWriter(file),
		replacer:       replacer,
	}
}

// WriteRow writes a single CSV row. All fields are unconditionally quoted.
// The internal builder is reused across calls to avoid per-row allocations.
func (writer *LadybugCSVWriter) WriteRow(fields []string) {
	writer.builder.Reset()
	for i, field := range fields {
		if i > 0 {
			writer.builder.WriteByte(',')
		}
		writer.builder.WriteByte('"')
		writer.replacer.WriteString(&writer.builder, field)
		writer.builder.WriteByte('"')
	}
	writer.builder.WriteByte('\n')
	writer.bufferedWriter.WriteString(writer.builder.String())
}

// Flush writes any buffered data to the underlying file. Must be called before closing the file.
func (writer *LadybugCSVWriter) Flush() error {
	return writer.bufferedWriter.Flush()
}
