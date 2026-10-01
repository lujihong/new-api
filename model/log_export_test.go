package model

import (
	"context"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestExportSnapshotKeysetAndProjection(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/export.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	old := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = old })
	if err = db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{11, 22, 33} {
		if err = db.Create(&Log{Id: id, UserId: 7, CreatedAt: 100, Other: `{"root_info":{"node_name":"node"}}`}).Error; err != nil {
			t.Fatal(err)
		}
	}
	f := LogExportFilter{Start: 90, End: 200, ViewerRole: 100}
	snapshot, total, err := SnapshotExportLogs(context.Background(), f)
	if err != nil || snapshot != 33 || total != 3 {
		t.Fatalf("snapshot=%d total=%d err=%v", snapshot, total, err)
	}
	f.SnapshotID = snapshot
	if err = db.Create(&Log{Id: 44, UserId: 7, CreatedAt: 100}).Error; err != nil {
		t.Fatal(err)
	}
	rows, more, _, err := ExportLogsPage(context.Background(), f, 0, 2, false)
	if err != nil || len(rows) != 2 || rows[0].Id != 33 || !more {
		t.Fatalf("first page %#v %v", rows, err)
	}
	if rows[0].Other != `{"root_info":{"node_name":"node"}}` {
		t.Fatalf("root stripped: %s", rows[0].Other)
	}
	rows, more, _, err = ExportLogsPage(context.Background(), f, rows[1].Id, 2, false)
	if err != nil || len(rows) != 1 || rows[0].Id != 11 || more {
		t.Fatalf("next page %#v %v", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err = ExportLogsPage(ctx, f, 0, 2, false); err == nil {
		t.Fatal("cancellation ignored")
	}
}
