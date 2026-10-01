package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/QuantumNous/new-api/common"
)

// Project once under the captured snapshot, collecting only UI-visible columns.
// The spool bounds memory independently of the number of exported rows.
type logExportSpool struct {
	file    *os.File
	buffer  *bufio.Writer
	encoder *json.Encoder
	headers []string
	seen    map[string]bool
	rows    int64
}

func newLogExportSpool() (*logExportSpool, error) {
	file, err := os.CreateTemp("", "new-api-projection-*.jsonl")
	if err != nil {
		return nil, err
	}
	buffer := bufio.NewWriter(file)
	return &logExportSpool{file: file, buffer: buffer, encoder: json.NewEncoder(buffer), headers: []string{"时间", "日志类型"}, seen: map[string]bool{"时间": true, "日志类型": true}}, nil
}
func (s *logExportSpool) close() { name := s.file.Name(); _ = s.file.Close(); _ = os.Remove(name) }
func (s *logExportSpool) append(ctx context.Context, fields logExportFields) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.rows >= common.MaxLogExportRows {
		return fmt.Errorf("export row limit exceeded")
	}
	row := make(map[string]any, len(fields))
	for _, f := range fields {
		if !s.seen[f.Name] {
			s.headers = append(s.headers, f.Name)
			s.seen[f.Name] = true
		}
		row[f.Name] = f.Value
	}
	if err := s.encoder.Encode(row); err != nil {
		return err
	}
	s.rows++
	return nil
}
func (s *logExportSpool) write(ctx context.Context, writer *common.XLSXRowWriter) error {
	if err := s.buffer.Flush(); err != nil {
		return err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	decoder := json.NewDecoder(bufio.NewReader(s.file))
	for index := int64(0); index < s.rows; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var fields map[string]any
		if err := decoder.Decode(&fields); err != nil {
			return err
		}
		row := make([]any, len(s.headers))
		for i, h := range s.headers {
			row[i] = fields[h]
		}
		if err := writer.SetRow(excelizeCell(int(index)+2), row); err != nil {
			return err
		}
	}
	return nil
}
