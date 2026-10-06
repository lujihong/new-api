package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

func parsePositiveIntQuery(c *gin.Context, name string, fallback int) (int, error) {
	value := c.Query(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func GetTokenLogPage(c *gin.Context) {
	start, end, err := common.ParseRequiredUnixSecondRange(c.Query("start_timestamp"), c.Query("end_timestamp"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	limit, err := parsePositiveIntQuery(c, "limit", model.TokenLogPageDefaultLimit)
	if err != nil || limit > model.TokenLogPageMaxLimit {
		if err == nil {
			err = fmt.Errorf("limit must be at most %d", model.TokenLogPageMaxLimit)
		}
		common.ApiError(c, err)
		return
	}
	snapshotID, err := parsePositiveIntQuery(c, "snapshot_id", 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	beforeID, err := parsePositiveIntQuery(c, "before_id", 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := model.GetTokenLogPage(c.Request.Context(), c.GetInt("id"), c.GetInt("token_id"), start, end, limit, snapshotID, beforeID, c.Query("skip_total") == "1" && beforeID > 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func parseLogExportRange(c *gin.Context) (int64, int64, error) {
	if c.Query("start_timestamp") != "" || c.Query("end_timestamp") != "" {
		return common.ParseRequiredUnixSecondRange(c.Query("start_timestamp"), c.Query("end_timestamp"))
	}
	return common.DateRangeFromPreset(c.Query("preset"), c.Query("history"), timeNow())
}

var timeNow = func() time.Time { return time.Now() }

func exportTaskStatusLabel(status string) string {
	switch status {
	case "SUCCESS":
		return "成功"
	case "FAILURE":
		return "失败"
	case "IN_PROGRESS":
		return "进行中"
	case "QUEUED", "SUBMITTED":
		return "排队中"
	case "NOT_START":
		return "未启动"
	case "UNKNOWN":
		return "未知"
	case "":
		return "提交中"
	default:
		return status
	}
}

func exportTaskActionLabel(action string) string {
	labels := map[string]string{"MUSIC": "生成音乐", "LYRICS": "生成歌词", "generate": "图生视频", "textGenerate": "文生视频", "firstTailGenerate": "首尾生视频", "referenceGenerate": "参照生视频", "remixGenerate": "视频混编"}
	if label, ok := labels[action]; ok {
		return label
	}
	return action
}

func exportLogTypeLabel(logType int) string {
	switch logType {
	case model.LogTypeTopup:
		return "充值"
	case model.LogTypeConsume:
		return "消费"
	case model.LogTypeManage:
		return "管理"
	case model.LogTypeSystem:
		return "系统"
	case model.LogTypeError:
		return "错误"
	case model.LogTypeRefund:
		return "退款"
	case model.LogTypeLogin:
		return "登录"
	default:
		return "未知"
	}
}

func exportOtherMap(raw string) map[string]any {
	var value map[string]any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return nil
	}
	return value
}

func exportString(value any) string {
	// Composite values must be explicitly projected; never stringify a map.
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func exportDuration(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	if seconds < 60 {
		return fmt.Sprintf("%.1f秒", float64(seconds))
	}
	return fmt.Sprintf("%d分%d秒", seconds/60, seconds%60)
}

func exportQuota(quota int, other map[string]any) string {
	value := exportMoney(float64(quota), true)
	if exportString(other["billing_source"]) == "subscription" {
		return "订阅扣费 " + value
	}
	return value
}

func GetLogsExport(c *gin.Context) {
	start, end, err := parseLogExportRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userID, isAdmin, _, err := resolveLogExportScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	role := common.RoleCommonUser
	if isAdmin {
		role = c.GetInt("role")
	}
	logType, _ := strconv.Atoi(c.Query("type"))
	filter := model.LogExportFilter{ViewerRole: role, UserID: userID, Start: start, End: end, LogType: logType, ModelName: c.Query("model_name"), Username: c.Query("username"), TokenName: c.Query("token_name"), Group: c.Query("group"), RequestID: c.Query("request_id"), UpstreamRequestID: c.Query("upstream_request_id")}
	filter.Channel, _ = strconv.Atoi(c.Query("channel"))
	snapshot, total, err := model.SnapshotExportLogs(c.Request.Context(), filter)
	filter.SnapshotID = snapshot
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if total > common.MaxLogExportRows {
		common.ApiError(c, fmt.Errorf("export contains %d rows; maximum is %d", total, common.MaxLogExportRows))
		return
	}
	headers, colWidths := commonLogExportHeaders(isAdmin && filter.ViewerRole >= common.RoleAdminUser)
	if err := writeXLSXStreamFile(c, "usage-logs.xlsx", "通用日志", start, end, total, common.XLSXStreamSheet{
		Name: "日志", Headers: headers, ColWidths: colWidths, Context: c.Request.Context(),
		WriteRowsChecked: func(writer *common.XLSXRowWriter) error {
			beforeID := 0
			rowNo := 2
			for total > 0 {
				logs, more, _, pageErr := model.ExportLogsPage(c.Request.Context(), filter, beforeID, model.ExportPageSize, false)
				if pageErr != nil {
					return pageErr
				}
				next := int64(0)
				if len(logs) > 0 {
					next = int64(logs[len(logs)-1].Id)
				}
				if err := checkExportPage(c, len(logs), int64(beforeID), next, more, int64(rowNo-2)); err != nil {
					return err
				}
				for _, log := range logs {
					row := formatCommonLogRow(log, exportOtherMap(log.Other), isAdmin && filter.ViewerRole >= common.RoleAdminUser)
					if err := writer.WriteRow(row); err != nil {
						return err
					}
					rowNo++
				}
				if len(logs) == 0 || !more {
					break
				}
				beforeID = int(next)
			}
			return nil
		},
	}); err != nil {
		common.ApiError(c, err)
	}
}

func GetTaskLogsExport(c *gin.Context) {
	start, end, err := parseLogExportRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userID, isAdmin, _, err := resolveLogExportScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	taskID, channelID := c.Query("task_id"), c.Query("channel_id")
	snapshot, total, err := model.SnapshotTaskExport(c.Request.Context(), userID, start, end, taskID, channelID, false)
	c.Request = c.Request.WithContext(model.WithExportSnapshot(c.Request.Context(), snapshot))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if total > common.MaxLogExportRows {
		common.ApiError(c, fmt.Errorf("export contains %d rows; maximum is %d", total, common.MaxLogExportRows))
		return
	}
	role := common.RoleCommonUser
	if isAdmin {
		role = c.GetInt("role")
	}
	headers, colWidths := taskLogExportHeaders(role >= common.RoleAdminUser)
	if err := writeXLSXStreamFile(c, "task-logs.xlsx", "任务日志", start, end, total, common.XLSXStreamSheet{
		Name: "任务日志", Headers: headers, ColWidths: colWidths, Context: c.Request.Context(),
		WriteRowsChecked: func(writer *common.XLSXRowWriter) error {
			beforeID := int64(0)
			rowNo := 2
			for total > 0 {
				rows, more, pageErr := model.ExportTaskLogsPage(c.Request.Context(), userID, start, end, taskID, channelID, beforeID, model.ExportPageSize)
				if pageErr != nil {
					return pageErr
				}
				next := int64(0)
				if len(rows) > 0 {
					next = int64(rows[len(rows)-1].ID)
				}
				if err := checkExportPage(c, len(rows), int64(beforeID), next, more, int64(rowNo-2)); err != nil {
					return err
				}
				for _, row := range rows {
					values := formatTaskLogRow(row, role >= common.RoleAdminUser)
					if err := writer.WriteRow(values); err != nil {
						return err
					}
					rowNo++
				}
				if len(rows) == 0 || !more {
					break
				}
				beforeID = rows[len(rows)-1].ID
			}
			return nil
		},
	}); err != nil {
		common.ApiError(c, err)
	}
}

func GetDrawingLogsExport(c *gin.Context) {
	start, end, err := parseLogExportRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userID, isAdmin, _, err := resolveLogExportScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	mjID, channelID := c.Query("mj_id"), c.Query("channel_id")
	snapshot, total, err := model.SnapshotTaskExport(c.Request.Context(), userID, start, end, mjID, channelID, true)
	c.Request = c.Request.WithContext(model.WithExportSnapshot(c.Request.Context(), snapshot))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if total > common.MaxLogExportRows {
		common.ApiError(c, fmt.Errorf("export contains %d rows; maximum is %d", total, common.MaxLogExportRows))
		return
	}
	role := common.RoleCommonUser
	if isAdmin {
		role = c.GetInt("role")
	}
	headers, colWidths := drawingLogExportHeaders(role >= common.RoleAdminUser)
	if err := writeXLSXStreamFile(c, "drawing-logs.xlsx", "绘图日志", start, end, total, common.XLSXStreamSheet{
		Name: "绘图日志", Headers: headers, ColWidths: colWidths, Context: c.Request.Context(),
		WriteRowsChecked: func(writer *common.XLSXRowWriter) error {
			beforeID := 0
			rowNo := 2
			for total > 0 {
				rows, more, pageErr := model.ExportDrawingLogsPage(c.Request.Context(), userID, start, end, mjID, channelID, beforeID, model.ExportPageSize)
				if pageErr != nil {
					return pageErr
				}
				next := int64(0)
				if len(rows) > 0 {
					next = int64(rows[len(rows)-1].ID)
				}
				if err := checkExportPage(c, len(rows), int64(beforeID), next, more, int64(rowNo-2)); err != nil {
					return err
				}
				for _, row := range rows {
					values := formatDrawingLogRow(row, role >= common.RoleAdminUser)
					if err := writer.WriteRow(values); err != nil {
						return err
					}
					rowNo++
				}
				if len(rows) == 0 || !more {
					break
				}
				beforeID = rows[len(rows)-1].ID
			}
			return nil
		},
	}); err != nil {
		common.ApiError(c, err)
	}
}

func excelizeCell(row int) string { return fmt.Sprintf("A%d", row) }

func writeXLSXStreamFile(c *gin.Context, filename, title string, start, end int64, total int64, sheets ...common.XLSXStreamSheet) error {
	temp, err := os.CreateTemp("", "new-api-export-*.xlsx")
	if err != nil {
		return fmt.Errorf("create export temp file: %w", err)
	}
	name := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	defer os.Remove(name)
	if _, err := common.BuildXLSXStreamFile(name, title, start, end, total, sheets...); err != nil {
		return err
	}
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(c.Writer, c.Request, filename, info.ModTime(), file)
	return nil
}

func writeXLSXFile(c *gin.Context, filename, title string, start, end int64, total int64, sheets ...common.XLSXSheet) error {
	streamSheets := make([]common.XLSXStreamSheet, 0, len(sheets))
	for _, sheet := range sheets {
		s := sheet
		streamSheets = append(streamSheets, common.XLSXStreamSheet{Name: s.Name, Headers: s.Headers, WriteRows: func(writer *excelize.StreamWriter) error {
			for rowIndex, row := range s.Rows {
				values := make([]any, len(row))
				for i, value := range row {
					if text, ok := value.(string); ok {
						values[i] = common.SafeExcelText(text)
					} else {
						values[i] = value
					}
				}
				cell, _ := excelize.CoordinatesToCellName(1, rowIndex+2)
				if err := writer.SetRow(cell, values); err != nil {
					return err
				}
			}
			return nil
		}})
	}
	temp, err := os.CreateTemp("", "new-api-export-*.xlsx")
	if err != nil {
		return fmt.Errorf("create export temp file: %w", err)
	}
	name := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	defer os.Remove(name)
	if _, err := common.BuildXLSXFile(name, title, start, end, total, sheets...); err != nil {
		return err
	}
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(c.Writer, c.Request, filename, info.ModTime(), file)
	return nil
}

func writeXLSX(c *gin.Context, filename string, data []byte) {
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data)
}
