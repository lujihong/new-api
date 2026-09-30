package common

import (
	"bytes"
	"fmt"
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
