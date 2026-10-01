package common

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const MaxLogExportRows int64 = 100000

// Limits are deliberately below ZIP64's 4 GiB/65535-entry boundaries.
const (
	xlsxMaxEntryBytes int64 = 256 << 20
	xlsxMaxRawBytes   int64 = 512 << 20
	xlsxMaxZipBytes   int64 = 128 << 20
	xlsxMaxSheets           = 8
	xlsxMaxRowBytes   int64 = 8 << 20
)

// SafeExcelText preserves display values. XLSX strings are inlineStr, never
// formulas; prefixing an apostrophe changes both text and negative amounts.
// This is not an escaping function for CSV exports.
func SafeExcelText(value string) string { return value }

type XLSXSheet struct {
	Name    string
	Headers []string
	Rows    [][]any
}

type XLSXStreamSheet struct {
	Name    string
	Headers []string
	// Deprecated: retained for source compatibility. The raw excelize writer
	// cannot intercept input before excelize truncates it. Use WriteRowsChecked
	// for strict input validation, cancellation and pre-spool byte budgets.
	WriteRows        func(*excelize.StreamWriter) error
	WriteRowsChecked func(*XLSXRowWriter) error
	Context          context.Context
}

// XLSXRowWriter is a synchronous, bounded writer. Do not retain it or use it
// concurrently. Errors are sticky even if a callback ignores a SetRow error.
type XLSXRowWriter struct {
	stream     *excelize.StreamWriter
	ctx        context.Context
	width, row int
	estimated  int64
	total      *int64
	err        error
}

func (w *XLSXRowWriter) WriteRow(values []any) error {
	return w.SetRow(cellName(1, w.row+1), values)
}

func (w *XLSXRowWriter) SetRow(cell string, values []any, opts ...excelize.RowOpts) error {
	if w.err != nil {
		return w.err
	}
	fail := func(err error) error { w.err = err; return err }
	if err := w.ctx.Err(); err != nil {
		return fail(err)
	}
	col, row, err := excelize.CellNameToCoordinates(cell)
	if err != nil {
		return fail(err)
	}
	if col != 1 || row != w.row+1 || row > excelize.TotalRows || int64(row-1) > MaxLogExportRows {
		return fail(fmt.Errorf("xlsx: invalid/nonconsecutive row %s", cell))
	}
	if len(values) != w.width {
		return fail(fmt.Errorf("xlsx: row %d width %d, expected %d", row, len(values), w.width))
	}
	cleaned := make([]any, len(values))
	estimate := int64(128)
	for i, v := range values {
		clean, size, err := xlsxValue(v)
		if err != nil {
			return fail(fmt.Errorf("xlsx: %s: %w", cellName(i+1, row), err))
		}
		cleaned[i] = clean
		estimate += size + 128
		if estimate > xlsxMaxRowBytes {
			return fail(errors.New("xlsx: row exceeds 8 MiB serialized budget"))
		}
	}
	if w.estimated+estimate > xlsxMaxEntryBytes || *w.total+estimate > xlsxMaxRawBytes {
		return fail(errors.New("xlsx: uncompressed budget exceeded before spooling"))
	}
	w.estimated += estimate
	*w.total += estimate
	if err := w.stream.SetRow(cell, cleaned, opts...); err != nil {
		return fail(err)
	}
	w.row = row
	return nil
}

func validateXLSXText(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("invalid UTF-8")
	}
	units, lines := 0, 0
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0xfffe || r == 0xffff {
			return errors.New("invalid XML character")
		}
		units++
		if r > 0xffff {
			units++
		}
		if r == '\n' {
			lines++
		}
		if units > excelize.TotalCellChars {
			return errors.New("cell exceeds 32767 UTF-16 units")
		}
		if lines > 253 {
			return errors.New("cell exceeds 253 line feeds")
		}
	}
	return nil
}

func xlsxValue(v any) (any, int64, error) {
	switch x := v.(type) {
	case excelize.Cell:
		if x.Formula != "" {
			return nil, 0, errors.New("formula cells are forbidden; pass formula-looking text as a string")
		}
		clean, size, err := xlsxValue(x.Value)
		x.Value = clean
		return x, size, err
	case *excelize.Cell:
		if x == nil {
			return nil, 0, nil
		}
		return xlsxValue(*x)
	case []excelize.RichTextRun:
		var text strings.Builder
		for _, run := range x {
			text.WriteString(run.Text)
		}
		if err := validateXLSXText(text.String()); err != nil {
			return nil, 0, err
		}
		return x, int64(text.Len())*6 + int64(len(x))*128, nil
	case []byte:
		return xlsxValue(string(x))
	case string:
		if err := validateXLSXText(x); err != nil {
			return nil, 0, err
		}
		return x, int64(len(x)) * 6, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, 0, errors.New("non-finite number")
		}
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil, 0, errors.New("non-finite number")
		}
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, time.Time, time.Duration:
	default:
		return nil, 0, fmt.Errorf("unsupported cell type %T", v)
	}
	return v, 64, nil
}

func BuildXLSXStreamFile(pathName, title string, start, end int64, rowCount int64, sheets ...XLSXStreamSheet) (size int64, err error) {
	if len(sheets) == 0 || len(sheets) > xlsxMaxSheets {
		return 0, errors.New("xlsx: expected 1..8 business sheets")
	}
	if rowCount < 0 || rowCount > MaxLogExportRows {
		return 0, errors.New("xlsx: row count exceeds export limit")
	}
	names := make(map[string]bool)
	for i := range sheets {
		// Do not modify the caller's variadic backing array.
		s := sheets[i]
		name := s.Name
		if name == "" {
			name = "日志"
		}
		if names[strings.ToLower(name)] {
			return 0, errors.New("xlsx: duplicate sheet name")
		}
		names[strings.ToLower(name)] = true
		if len(s.Headers) == 0 || len(s.Headers) > excelize.MaxColumns {
			return 0, errors.New("xlsx: invalid header width")
		}
		for _, h := range s.Headers {
			if err := validateXLSXText(h); err != nil {
				return 0, fmt.Errorf("xlsx header: %w", err)
			}
		}
		if s.WriteRows != nil && s.WriteRowsChecked != nil {
			return 0, errors.New("xlsx: select exactly one row callback")
		}
		if s.Context != nil && s.Context.Err() != nil {
			return 0, s.Context.Err()
		}
	}
	target := filepath.Clean(pathName)
	// Private directory keeps excelize spill files private and permits unconditional
	// cleanup after callback errors, cancellation or partial ZIP writes.
	dir, err := os.MkdirTemp(filepath.Dir(target), ".xlsx-export-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	file, err := os.OpenFile(filepath.Join(dir, "output.xlsx"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	book := excelize.NewFile(excelize.Options{TmpDir: dir})
	defer book.Close()
	zw := newFileZipWriter(file)
	for _, s := range sheets {
		if s.Context != nil {
			zw.contexts = append(zw.contexts, s.Context)
		}
	}
	// Excelize internally creates an empty buffer; our writer deliberately ignores
	// it. All ZIP bytes go straight to disk, and limits prohibit ZIP64 fixups.
	book.SetZipWriter(func(io.Writer) excelize.ZipWriter { return zw })
	var total int64
	for i, s := range sheets {
		name := s.Name
		if name == "" {
			name = "日志"
		}
		if i == 0 {
			err = book.SetSheetName(book.GetSheetName(0), name)
		} else {
			_, err = book.NewSheet(name)
		}
		if err != nil {
			return 0, err
		}
		sw, err := book.NewStreamWriter(name)
		if err != nil {
			return 0, err
		}
		ctx := s.Context
		if ctx == nil {
			ctx = context.Background()
		}
		rw := &XLSXRowWriter{stream: sw, ctx: ctx, width: len(s.Headers), total: &total}
		header := make([]any, len(s.Headers))
		for j, h := range s.Headers {
			header[j] = h
		}
		if err := rw.SetRow("A1", header); err != nil {
			return 0, err
		}
		if s.WriteRowsChecked != nil {
			if err := s.WriteRowsChecked(rw); err != nil {
				return 0, err
			}
			if rw.err != nil {
				return 0, rw.err
			}
		} else if s.WriteRows != nil {
			if err := s.WriteRows(sw); err != nil {
				return 0, err
			}
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := sw.Flush(); err != nil {
			return 0, err
		}
	}
	if err := book.Write(io.Discard); err != nil {
		return 0, err
	}
	if zw.err != nil {
		return 0, zw.err
	}
	if err := book.Close(); err != nil {
		return 0, err
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	// Legacy callbacks bypass preflight checks: inspect XML without loading the
	// workbook in memory. Never publish formula-bearing or malformed worksheets.
	if err := validateXLSXArchive(file.Name(), sheets); err != nil {
		return 0, err
	}
	for _, s := range sheets {
		if s.Context != nil && s.Context.Err() != nil {
			return 0, s.Context.Err()
		}
	}
	// Atomically replace only on success; failed exports preserve existing files.
	if err := os.Rename(file.Name(), target); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

type fileZipWriter struct {
	zip      *zip.Writer
	out      *xlsxLimitedWriter
	total    int64
	err      error
	entries  int
	contexts []context.Context
}
type xlsxLimitedWriter struct {
	dst         io.Writer
	used, limit int64
	owner       *fileZipWriter
	aggregate   bool
}

func (w *xlsxLimitedWriter) Write(p []byte) (int, error) {
	for _, ctx := range w.owner.contexts {
		if err := ctx.Err(); err != nil {
			w.owner.err = err
			return 0, err
		}
	}
	if w.owner.err != nil {
		return 0, w.owner.err
	}
	if int64(len(p)) > w.limit-w.used || w.aggregate && int64(len(p)) > xlsxMaxRawBytes-w.owner.total {
		w.owner.err = errors.New("xlsx: ZIP size budget exceeded")
		return 0, w.owner.err
	}
	n, err := w.dst.Write(p)
	w.used += int64(n)
	if w.aggregate {
		w.owner.total += int64(n)
	}
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.owner.err = err
	}
	return n, err
}
func newFileZipWriter(dst io.Writer) *fileZipWriter {
	w := &fileZipWriter{}
	w.out = &xlsxLimitedWriter{dst: dst, limit: xlsxMaxZipBytes, owner: w}
	w.zip = zip.NewWriter(w.out)
	return w
}
func (w *fileZipWriter) Create(name string) (io.Writer, error) {
	if w.err != nil {
		return nil, w.err
	}
	if w.entries >= 1024 {
		w.err = errors.New("xlsx: ZIP entry count limit")
		return nil, w.err
	}
	w.entries++
	dst, err := w.zip.Create(name)
	if err != nil {
		w.err = err
		return nil, err
	}
	return &xlsxLimitedWriter{dst: dst, limit: xlsxMaxEntryBytes, owner: w, aggregate: true}, nil
}
func (w *fileZipWriter) AddFS(fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("xlsx: nonregular ZIP entry")
		}
		src, err := fsys.Open(name)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := w.Create(name)
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, src)
		return err
	})
}
func (w *fileZipWriter) Close() error {
	err := w.zip.Close()
	if w.err != nil {
		return w.err
	}
	w.err = err
	return err
}

func validateXLSXArchive(filename string, sheets []XLSXStreamSheet) error {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer z.Close()
	seen := 0
	for _, f := range z.File {
		if f.CompressedSize64 > uint64(xlsxMaxZipBytes) || f.UncompressedSize64 > uint64(xlsxMaxEntryBytes) {
			return errors.New("xlsx: oversized ZIP entry")
		}
		if !strings.HasPrefix(f.Name, "xl/worksheets/sheet") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		idx, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(f.Name, "xl/worksheets/sheet"), ".xml"))
		if err != nil || idx < 1 || idx > len(sheets) {
			return errors.New("xlsx: unexpected business sheet")
		}
		seen++
		// Checked callbacks already validate every input before serialization.
		if sheets[idx-1].WriteRows == nil {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		err = validateLegacySheet(src, len(sheets[idx-1].Headers), sheets[idx-1].Context)
		closeErr := src.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if seen != len(sheets) {
		return errors.New("xlsx: missing business sheet")
	}
	return nil
}
func validateLegacySheet(src io.Reader, width int, ctx context.Context) error {
	d := xml.NewDecoder(src)
	lastRow := 0
	for {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		e, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch e.Name.Local {
		case "f":
			return errors.New("xlsx: formulas are forbidden")
		case "row":
			for _, a := range e.Attr {
				if a.Name.Local == "r" {
					r, err := strconv.Atoi(a.Value)
					if err != nil || r != lastRow+1 || int64(r-1) > MaxLogExportRows || r > excelize.TotalRows {
						return errors.New("xlsx: invalid row")
					}
					lastRow = r
				}
			}
		case "c":
			for _, a := range e.Attr {
				if a.Name.Local == "r" {
					c, r, err := excelize.CellNameToCoordinates(a.Value)
					if err != nil || c > width || r != lastRow {
						return errors.New("xlsx: cell outside header width/row")
					}
				}
			}
		case "t":
			var text string
			if err := d.DecodeElement(&text, &e); err != nil {
				return err
			}
			if err := validateXLSXText(text); err != nil {
				return err
			}
			// Fail closed at both possible UTF-16 truncation boundaries.
			// Exact-limit values require the checked API.
			units := 0
			for _, r := range text {
				units++
				if r > 0xffff {
					units++
				}
			}
			if units >= excelize.TotalCellChars-1 {
				return errors.New("xlsx: legacy cell may be truncated; use WriteRowsChecked")
			}
		}
	}
}

func BuildXLSXFile(pathName, title string, start, end int64, rowCount int64, sheets ...XLSXSheet) (int64, error) {
	streams := make([]XLSXStreamSheet, len(sheets))
	for i, s := range sheets {
		streams[i] = XLSXStreamSheet{Name: s.Name, Headers: s.Headers, WriteRowsChecked: func(w *XLSXRowWriter) error {
			for _, r := range s.Rows {
				if err := w.WriteRow(r); err != nil {
					return err
				}
			}
			return nil
		}}
	}
	return BuildXLSXStreamFile(pathName, title, start, end, rowCount, streams...)
}

// BuildXLSX necessarily allocates the returned []byte. Large callers should use
// BuildXLSXStreamFile; serialization itself still uses the same disk ZIP path.
func BuildXLSX(title string, start, end int64, rowCount int64, sheets ...XLSXSheet) ([]byte, error) {
	dir, err := os.MkdirTemp("", "xlsx-bytes-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	name := filepath.Join(dir, "export.xlsx")
	if _, err := BuildXLSXFile(name, title, start, end, rowCount, sheets...); err != nil {
		return nil, err
	}
	return os.ReadFile(name)
}
func cellName(column, row int) string {
	name, _ := excelize.ColumnNumberToName(column)
	return fmt.Sprintf("%s%d", name, row)
}
