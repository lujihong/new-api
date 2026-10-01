package common

import (
	"archive/zip"
	"path/filepath"
	"testing"
)

func TestBuildXLSXFileWritesReadableWorkbook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.xlsx")
	if _, err := BuildXLSXFile(path, "测试导出", 1704067200, 1704153600, 1, XLSXSheet{
		Name:    "日志",
		Headers: []string{"时间", "详情"},
		Rows:    [][]any{{"2024-01-01 08:00:00", "普通文本"}},
	}); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	seen := map[string]bool{}
	for _, entry := range archive.File {
		seen[entry.Name] = true
	}
	if !seen["xl/workbook.xml"] || !seen["xl/worksheets/sheet1.xml"] {
		t.Fatalf("missing workbook parts: %v", seen)
	}
}
