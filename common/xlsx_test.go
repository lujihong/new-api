package common

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func checkWorkbook(t *testing.T, name string, want [][]string) {
	t.Helper()
	b, err := excelize.OpenFile(name)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !reflect.DeepEqual(b.GetSheetList(), []string{"日志"}) {
		t.Fatalf("unexpected sheets %v", b.GetSheetList())
	}
	rows, err := b.GetRows("日志")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("GetRows mismatch: got %v want %v", rows, want)
	}
	for r, row := range want {
		for c := range row {
			f, err := b.GetCellFormula("日志", cellName(c+1, r+1))
			if err != nil || f != "" {
				t.Fatalf("formula %q %v", f, err)
			}
		}
	}
	z, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	found := false
	for _, f := range z.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rd, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(rd)
			rd.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte(`t="inlineStr"`)) || bytes.Contains(data, []byte("<f>")) {
				t.Fatalf("invalid string serialization: %.200s", data)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing sheet XML")
	}
	st, err := os.Stat(name)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", st, err)
	}
}

func TestXLSXAllPaths(t *testing.T) {
	headers := []string{"时间", "详情", "金额"}
	values := []any{"2026-10-01 08:00:00", "=HYPERLINK(\"https://invalid\")", -12.5}
	want := [][]string{headers, {"2026-10-01 08:00:00", "=HYPERLINK(\"https://invalid\")", "-12.5"}}
	for _, mode := range []string{"file", "bytes", "checked", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "export.xlsx")
			sheet := XLSXSheet{Name: "日志", Headers: headers, Rows: [][]any{values}}
			var err error
			switch mode {
			case "file":
				_, err = BuildXLSXFile(name, "ignored", 0, 0, 1, sheet)
			case "bytes":
				var data []byte
				data, err = BuildXLSX("ignored", 0, 0, 1, sheet)
				if err == nil {
					err = os.WriteFile(name, data, 0600)
				}
			case "checked":
				_, err = BuildXLSXStreamFile(name, "", 0, 0, 1, XLSXStreamSheet{Name: "日志", Headers: headers, WriteRowsChecked: func(w *XLSXRowWriter) error { return w.WriteRow(values) }})
			case "legacy":
				_, err = BuildXLSXStreamFile(name, "", 0, 0, 1, XLSXStreamSheet{Name: "日志", Headers: headers, WriteRows: func(w *excelize.StreamWriter) error { return w.SetRow("A2", values) }})
			}
			if err != nil {
				t.Fatal(err)
			}
			checkWorkbook(t, name, want)
		})
	}
}

func TestXLSXTextAndEmpty(t *testing.T) {
	for _, text := range []string{"=1+1", "+SUM(A1)", "-12.50", "@cmd", " \t=1", "中文😀", strings.Repeat("中", 32767), strings.Repeat("😀", 16383) + "中", "_x0000_", "a\r\nb"} {
		name := filepath.Join(t.TempDir(), "e.xlsx")
		if SafeExcelText(text) != text {
			t.Fatal("text modified")
		}
		if _, err := BuildXLSXFile(name, "", 0, 0, 1, XLSXSheet{Headers: []string{"详情"}, Rows: [][]any{{text}}}); err != nil {
			t.Fatal(err)
		}
		checkWorkbook(t, name, [][]string{{"详情"}, {text}})
	}
	name := filepath.Join(t.TempDir(), "empty.xlsx")
	if _, err := BuildXLSXFile(name, "", 0, 0, 0, XLSXSheet{Headers: []string{"详情"}}); err != nil {
		t.Fatal(err)
	}
	checkWorkbook(t, name, [][]string{{"详情"}})
	if _, err := BuildXLSXFile(name, "", 0, 0, 0); err == nil {
		t.Fatal("missing sheets accepted")
	}
}

func TestXLSXValidationAndCleanup(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"long", strings.Repeat("中", 32768)}, {"utf16", strings.Repeat("😀", 16384)},
		{"control", "a\x00b"}, {"invalidUTF8", string([]byte{255})}, {"linefeeds", strings.Repeat("\n", 254)},
		{"formula", excelize.Cell{Formula: "1+1", Value: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			name := filepath.Join(dir, "out.xlsx")
			_, err := BuildXLSXFile(name, "", 0, 0, 1, XLSXSheet{Headers: []string{"头"}, Rows: [][]any{{tc.value}}})
			if err == nil {
				t.Fatal("invalid data accepted")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("leak %v", entries)
			}
		})
	}
	for _, row := range [][]any{{}, {1, 2}} {
		if _, err := BuildXLSXFile(filepath.Join(t.TempDir(), "e.xlsx"), "", 0, 0, 1, XLSXSheet{Headers: []string{"头"}, Rows: [][]any{row}}); err == nil {
			t.Fatal("width accepted")
		}
	}
	for _, headers := range [][]string{nil, make([]string, excelize.MaxColumns+1), {strings.Repeat("a", 32768)}} {
		if _, err := BuildXLSXFile(filepath.Join(t.TempDir(), "e.xlsx"), "", 0, 0, 0, XLSXSheet{Headers: headers}); err == nil {
			t.Fatal("header accepted")
		}
	}
	if _, err := BuildXLSXFile(filepath.Join(t.TempDir(), "e.xlsx"), "", 0, 0, MaxLogExportRows+1, XLSXSheet{Headers: []string{"a"}}); err == nil {
		t.Fatal("row count accepted")
	}
}

func TestXLSXCancelFailureAndSticky(t *testing.T) {
	for _, mode := range []string{"cancel", "callback", "sticky", "rowlimit", "rename", "legacyFormula", "legacyWidth"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			name := filepath.Join(dir, "out.xlsx")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := XLSXStreamSheet{Headers: []string{"头"}, Context: ctx, WriteRowsChecked: func(w *XLSXRowWriter) error {
				switch mode {
				case "cancel":
					cancel()
					return w.WriteRow([]any{"x"})
				case "callback":
					return io.ErrClosedPipe
				case "sticky":
					_ = w.WriteRow([]any{1, 2})
					return nil
				case "rowlimit":
					return w.SetRow("A1048577", []any{1})
				}
				return w.WriteRow([]any{"x"})
			}}
			if mode == "rename" {
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "legacy") {
				s.WriteRowsChecked = nil
				s.WriteRows = func(w *excelize.StreamWriter) error {
					if mode == "legacyFormula" {
						return w.SetRow("A2", []any{excelize.Cell{Formula: "1+1"}})
					}
					return w.SetRow("A2", []any{1, 2})
				}
			}
			_, err := BuildXLSXStreamFile(name, "", 0, 0, 1, s)
			if err == nil {
				t.Fatal("failure accepted")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".xlsx-export-") {
					t.Fatal("temp leak")
				}
			}
			if mode != "rename" {
				if _, err := os.Stat(name); !os.IsNotExist(err) {
					t.Fatal("partial output remains")
				}
			}
		})
	}
	dir := t.TempDir()
	name := filepath.Join(dir, "existing.xlsx")
	os.WriteFile(name, []byte("original"), 0600)
	_, err := BuildXLSXFile(name, "", 0, 0, 1, XLSXSheet{Headers: []string{"h"}, Rows: [][]any{{strings.Repeat("x", 32768)}}})
	got, _ := os.ReadFile(name)
	if err == nil || string(got) != "original" {
		t.Fatal("failed export replaced existing output")
	}
}

type xlsxFailWriter struct{}

func (xlsxFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestXLSXZIPLimitsAndWriteFailure(t *testing.T) {
	for _, mode := range []string{"compressed", "entry", "aggregate", "io", "entries"} {
		t.Run(mode, func(t *testing.T) {
			var dst io.Writer = io.Discard
			if mode == "io" {
				dst = xlsxFailWriter{}
			}
			z := newFileZipWriter(dst)
			if mode == "compressed" {
				z.out.limit = 8
			}
			if mode == "entries" {
				z.entries = 1024
				if _, err := z.Create("x"); err == nil {
					t.Fatal("entry count")
				}
				return
			}
			w, err := z.Create("x")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "entry" {
				w.(*xlsxLimitedWriter).limit = 2
			}
			if mode == "aggregate" {
				z.total = xlsxMaxRawBytes - 1
			}
			_, writeErr := w.Write([]byte("test content"))
			closeErr := z.Close()
			if writeErr == nil && closeErr == nil {
				t.Fatal("budget/IO error lost")
			}
		})
	}
}

// Run each size in a fresh test process with XLSX_BENCH_ROWS. Input batches are
// bounded at 250 rows. Generation measurements stop BEFORE OpenFile/GetRows,
// since GetRows intentionally materializes every row and is not the exporter.
func TestXLSXBatchProfile(t *testing.T) {
	raw := os.Getenv("XLSX_BENCH_ROWS")
	if raw == "" {
		t.Skip("opt-in local profile")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > int(MaxLogExportRows) {
		t.Fatal("invalid benchmark rows")
	}
	name := filepath.Join(t.TempDir(), "profile.xlsx")
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	baseRSS := usage.Maxrss
	var peak atomic.Uint64
	peak.Store(baseline.HeapAlloc)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak.Load() {
					peak.Store(m.HeapAlloc)
				}
			}
		}
	}()
	start := time.Now()
	size, err := BuildXLSXStreamFile(name, "", 0, 0, int64(n), XLSXStreamSheet{Headers: []string{"时间", "用户", "模型", "金额", "详情", "请求ID"}, WriteRowsChecked: func(w *XLSXRowWriter) error {
		for base := 0; base < n; base += 250 {
			count := min(250, n-base)
			batch := make([][]any, count)
			for j := range batch {
				i := base + j
				batch[j] = []any{"2026-10-01 12:34:56", fmt.Sprintf("用户-%d", i), "中文模型", -0.125, strings.Repeat("真实完整详情", 12), fmt.Sprintf("request-%09d", i)}
			}
			for _, row := range batch {
				if err := w.WriteRow(row); err != nil {
					return err
				}
			}
		}
		return nil
	}})
	elapsed := time.Since(start)
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	var final runtime.MemStats
	runtime.ReadMemStats(&final)
	if final.HeapAlloc > peak.Load() {
		peak.Store(final.HeapAlloc)
	}
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	rssUnit := int64(1024)
	if runtime.GOOS == "darwin" {
		rssUnit = 1
	}
	peakRSS := usage.Maxrss * rssUnit
	rssDelta := (usage.Maxrss - baseRSS) * rssUnit
	t.Logf("PROFILE rows=%d batch=250 elapsed_ms=%d bytes=%d heap_base=%d heap_peak=%d heap_delta=%d rss_base=%d rss_peak=%d rss_delta=%d alloc_total=%d", n, elapsed.Milliseconds(), size, baseline.HeapAlloc, peak.Load(), peak.Load()-baseline.HeapAlloc, baseRSS*rssUnit, peakRSS, rssDelta, final.TotalAlloc-baseline.TotalAlloc)
	if peak.Load()-baseline.HeapAlloc > 128<<20 || rssDelta > 128<<20 {
		t.Errorf("generation exceeds 128 MiB incremental memory target")
	}
	book, err := excelize.OpenFile(name)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows("日志")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != n+1 || rows[0][0] != "时间" || rows[n][5] != fmt.Sprintf("request-%09d", n-1) || rows[n][3] != "-0.125" {
		t.Fatal("real excelize roundtrip failed")
	}
	t.Logf("ROUNDTRIP rows=%d header/lastRow/negative amount verified", len(rows))
}

func TestXLSXPreSpoolBudgetsAndLegacyTruncation(t *testing.T) {
	for _, mode := range []string{"row", "entry", "total", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			s := XLSXStreamSheet{Headers: []string{"头"}, WriteRowsChecked: func(w *XLSXRowWriter) error {
				if mode == "entry" {
					w.estimated = xlsxMaxEntryBytes
				}
				if mode == "total" {
					*w.total = xlsxMaxRawBytes
				}
				return w.WriteRow([]any{"test"})
			}}
			if mode == "row" {
				s.Headers = make([]string, 50)
				s.WriteRowsChecked = func(w *XLSXRowWriter) error {
					row := make([]any, 50)
					for i := range row {
						row[i] = strings.Repeat("中", 32767)
					}
					return w.WriteRow(row)
				}
			}
			if mode == "legacy" {
				s.WriteRowsChecked = nil
				s.WriteRows = func(w *excelize.StreamWriter) error { return w.SetRow("A2", []any{strings.Repeat("中", 32768)}) }
			}
			if _, err := BuildXLSXStreamFile(filepath.Join(dir, "out.xlsx"), "", 0, 0, 1, s); err == nil {
				t.Fatal("invalid export accepted")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal("partial export leaked")
			}
		})
	}
}

func TestXLSXOnlyBusinessSheets(t *testing.T) {
	name := filepath.Join(t.TempDir(), "multi.xlsx")
	if _, err := BuildXLSXFile(name, "must not create metadata", 0, 0, 0, XLSXSheet{Name: "日志", Headers: []string{"时间"}}, XLSXSheet{Name: "任务", Headers: []string{"状态"}}); err != nil {
		t.Fatal(err)
	}
	b, err := excelize.OpenFile(name)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !reflect.DeepEqual(b.GetSheetList(), []string{"日志", "任务"}) {
		t.Fatal(b.GetSheetList())
	}
	rows, err := b.GetRows("任务")
	if err != nil || !reflect.DeepEqual(rows, [][]string{{"状态"}}) {
		t.Fatalf("second sheet header lost: %v %v", rows, err)
	}
}
