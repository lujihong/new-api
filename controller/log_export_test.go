package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func TestExportScopeFailClosed(t *testing.T) {
	for _, tc := range []struct {
		scope                string
		role, id             int
		admin, root, wantErr bool
	}{
		{"self", common.RoleRootUser, 8, false, false, false},
		{"all", common.RoleAdminUser, 8, true, false, false},
		{"all", common.RoleRootUser, 8, true, true, false},
		{"all", common.RoleCommonUser, 8, false, false, true},
		{"", common.RoleRootUser, 8, false, false, false},
		{"typo", common.RoleRootUser, 8, false, false, true},
		{"self", common.RoleCommonUser, 0, false, false, true},
		{"all", common.RoleRootUser, 0, false, false, true},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?scope="+tc.scope, nil)
		c.Set("role", tc.role)
		c.Set("id", tc.id)
		uid, admin, root, err := resolveLogExportScope(c)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%+v err=%v", tc, err)
		}
		if err != nil {
			continue
		}
		if admin != tc.admin || root != tc.root || (!admin && uid != tc.id) {
			t.Fatalf("%+v got uid=%d admin=%v root=%v", tc, uid, admin, root)
		}
	}
}

func TestCommonExportPrivateColumns(t *testing.T) {
	headers := commonExportHeaders(false, false)
	for _, h := range headers {
		if h == "请求路径" || h == "节点名称" || h == "渠道编号" {
			t.Fatalf("private column %s", h)
		}
	}
	row := exportCommonRow(&model.Log{Other: `{"audit_info":{"path":"SECRET"},"request_path":"SECRET","root_info":{"node_name":"SECRET"}}`}, false, false)
	if len(row) != len(headers) {
		t.Fatalf("row/header width %d/%d", len(row), len(headers))
	}
	for _, v := range row {
		if s, ok := v.(string); ok && strings.Contains(s, "SECRET") {
			t.Fatal("private data leaked")
		}
	}
}

func TestDrawingExportUIContract(t *testing.T) {
	headers := drawingExportHeaders(true)
	for _, h := range headers {
		if h == "费用" {
			t.Fatal("drawing UI has no fee")
		}
	}
	row := drawingExportValues(model.DrawingExportRow{Action: "IMAGINE", Status: "NOT_START", Code: 21}, true)
	if len(row) != len(headers) {
		t.Fatal("width mismatch")
	}
	if row[1] != "绘图" || row[2] != "未启动" || row[len(row)-1] != "等待中" {
		t.Fatalf("unlocalized row: %#v", row)
	}
}
