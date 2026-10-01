package common

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

const MaxLogExportRows int64 = 100000

var formulaPrefix = regexp.MustCompile(`^[=+\-@]`)

func SafeExcelText(value string) string {
	if formulaPrefix.MatchString(strings.TrimSpace(value)) {
		return "'" + value
	}
	return value
}

type XLSXSheet struct {
	Name    string
	Headers []string
	Rows    [][]any
}

type XLSXStreamSheet struct {
	Name      string
	Headers   []string
	WriteRows func(*excelize.StreamWriter) error
}

func BuildXLSXStreamFile(pathName, title string, start, end int64, rowCount int64, sheets ...XLSXStreamSheet) (int64, error) {
	file, err := os.OpenFile(path.Clean(pathName), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	book := excelize.NewFile()
	defer book.Close()
	book.SetZipWriter(func(io.Writer) excelize.ZipWriter { return &fileZipWriter{file: file, zip: zip.NewWriter(file)} })
	meta := book.GetSheetName(0)
	if err := book.SetSheetName(meta, "筛选信息"); err != nil {
		return 0, err
	}
	metaRows := [][]any{{"导出标题", SafeExcelText(title)}, {"筛选开始（含）", BeijingDateTime(start)}, {"筛选结束（不含）", BeijingDateTime(end)}, {"时区", "Asia/Shanghai (Beijing)"}, {"行数", rowCount}, {"生成时间", BeijingDateTime(time.Now().Unix())}}
	for r, row := range metaRows {
		for c, value := range row {
			if err := book.SetCellValue("筛选信息", cellName(c+1, r+1), value); err != nil {
				return 0, err
			}
		}
	}
	for _, sheet := range sheets {
		name := sheet.Name
		if name == "" {
			name = "日志"
		}
		if _, err := book.NewSheet(name); err != nil {
			return 0, err
		}
		stream, err := book.NewStreamWriter(name)
		if err != nil {
			return 0, err
		}
		header := make([]any, len(sheet.Headers))
		for i, value := range sheet.Headers {
			header[i] = SafeExcelText(value)
		}
		if err := stream.SetRow("A1", header); err != nil {
			return 0, err
		}
		if sheet.WriteRows != nil {
			if err := sheet.WriteRows(stream); err != nil {
				return 0, err
			}
		}
		if err := stream.Flush(); err != nil {
			return 0, err
		}
	}
	book.DeleteSheet("Sheet1")
	if err := book.Write(io.Discard); err != nil {
		return 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

type fileZipWriter struct {
	file *os.File
	zip  *zip.Writer
}

func (w *fileZipWriter) Create(name string) (io.Writer, error) { return w.zip.Create(name) }
func (w *fileZipWriter) AddFS(fsys fs.FS) error                { return w.zip.AddFS(fsys) }
func (w *fileZipWriter) Close() error {
	if err := w.zip.Close(); err != nil {
		return err
	}
	return w.file.Sync()
}

func BuildXLSXFile(pathName, title string, start, end int64, rowCount int64, sheets ...XLSXSheet) (int64, error) {
	file, err := os.OpenFile(path.Clean(pathName), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	book := excelize.NewFile()
	defer book.Close()
	book.SetZipWriter(func(io.Writer) excelize.ZipWriter { return &fileZipWriter{file: file, zip: zip.NewWriter(file)} })
	meta := book.GetSheetName(0)
	if err := book.SetSheetName(meta, "筛选信息"); err != nil {
		return 0, err
	}
	metaRows := [][]any{{"导出标题", SafeExcelText(title)}, {"筛选开始（含）", BeijingDateTime(start)}, {"筛选结束（不含）", BeijingDateTime(end)}, {"时区", "Asia/Shanghai (Beijing)"}, {"行数", rowCount}, {"生成时间", BeijingDateTime(time.Now().Unix())}}
	for r, row := range metaRows {
		for c, value := range row {
			if err := book.SetCellValue("筛选信息", cellName(c+1, r+1), value); err != nil {
				return 0, err
			}
		}
	}
	for _, sheet := range sheets {
		name := sheet.Name
		if name == "" {
			name = "日志"
		}
		if _, err := book.NewSheet(name); err != nil {
			return 0, err
		}
		for c, header := range sheet.Headers {
			if err := book.SetCellValue(name, cellName(c+1, 1), SafeExcelText(header)); err != nil {
				return 0, err
			}
		}
		stream, err := book.NewStreamWriter(name)
		if err != nil {
			return 0, err
		}
		for r, row := range sheet.Rows {
			values := make([]any, len(row))
			for c, value := range row {
				if text, ok := value.(string); ok {
					values[c] = SafeExcelText(text)
				} else {
					values[c] = value
				}
			}
			if err := stream.SetRow(cellName(1, r+2), values); err != nil {
				return 0, err
			}
		}
		if err := stream.Flush(); err != nil {
			return 0, err
		}
	}
	book.DeleteSheet("Sheet1")
	if err := book.Write(io.Discard); err != nil {
		return 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func BuildXLSX(title string, start, end int64, rowCount int64, sheets ...XLSXSheet) ([]byte, error) {
	file := excelize.NewFile()
	defer file.Close()
	meta := file.GetSheetName(0)
	if err := file.SetSheetName(meta, "筛选信息"); err != nil {
		return nil, err
	}
	metaRows := [][]any{
		{"导出标题", SafeExcelText(title)},
		{"筛选开始（含）", BeijingDateTime(start)},
		{"筛选结束（不含）", BeijingDateTime(end)},
		{"时区", "Asia/Shanghai (Beijing)"},
		{"行数", rowCount},
		{"生成时间", BeijingDateTime(time.Now().Unix())},
	}
	for r, row := range metaRows {
		for c, value := range row {
			if err := file.SetCellValue("筛选信息", cellName(c+1, r+1), value); err != nil {
				return nil, err
			}
		}
	}
	for _, sheet := range sheets {
		name := sheet.Name
		if name == "" {
			name = "日志"
		}
		if _, err := file.NewSheet(name); err != nil {
			return nil, err
		}
		for c, header := range sheet.Headers {
			if err := file.SetCellValue(name, cellName(c+1, 1), SafeExcelText(header)); err != nil {
				return nil, err
			}
		}
		for r, row := range sheet.Rows {
			for c, value := range row {
				if text, ok := value.(string); ok {
					value = SafeExcelText(text)
				}
				if err := file.SetCellValue(name, cellName(c+1, r+2), value); err != nil {
					return nil, err
				}
			}
		}
	}
	file.DeleteSheet("Sheet1")
	var output bytes.Buffer
	if err := file.Write(&output); err != nil {
		return nil, fmt.Errorf("write xlsx: %w", err)
	}
	return output.Bytes(), nil
}

func cellName(column, row int) string {
	name, _ := excelize.ColumnNumberToName(column)
	return fmt.Sprintf("%s%d", name, row)
}
