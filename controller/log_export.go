package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
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
	result, err := model.GetTokenLogPage(c.Request.Context(), c.GetInt("id"), c.GetInt("token_id"), start, end, limit, snapshotID, beforeID)
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

func GetLogsExport(c *gin.Context) {
	start, end, err := parseLogExportRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	role := c.GetInt("role")
	userID := 0
	if role < common.RoleAdminUser {
		userID = c.GetInt("id")
	}
	logType, _ := strconv.Atoi(c.Query("type"))
	filter := model.LogExportFilter{UserID: userID, Start: start, End: end, LogType: logType, ModelName: c.Query("model_name"), Username: c.Query("username"), TokenName: c.Query("token_name"), Group: c.Query("group"), RequestID: c.Query("request_id"), UpstreamRequestID: c.Query("upstream_request_id")}
	filter.Channel, _ = strconv.Atoi(c.Query("channel"))
	logs, total, err := model.ExportLogs(c.Request.Context(), filter, common.MaxLogExportRows)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rows := make([][]any, 0, len(logs))
	for _, log := range logs {
		taskID := ""
		var other map[string]any
		if json.Unmarshal([]byte(log.Other), &other) == nil {
			if value, ok := other["task_id"].(string); ok {
				taskID = value
			}
		}
		rows = append(rows, []any{log.Id, log.UserId, log.Username, log.CreatedAt, common.BeijingISOTime(log.CreatedAt), log.Type, log.TokenName, log.ModelName, taskID, log.Quota, log.PromptTokens, log.CompletionTokens, log.UseTime, log.IsStream, log.ChannelId, log.Group, log.RequestId, log.UpstreamRequestId, log.Content, log.Other})
	}
	data, err := common.BuildXLSX("API usage logs", start, end, total, common.XLSXSheet{Name: "日志", Headers: []string{"ID", "User ID", "Username", "Created At (Unix)", "Created At (Beijing)", "Type", "Token Name", "Model Name", "Task ID", "Quota", "Prompt Tokens", "Completion Tokens", "Use Time", "Stream", "Channel ID", "Group", "Request ID", "Upstream Request ID", "Content", "Other"}, Rows: rows})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	writeXLSX(c, "usage-logs.xlsx", data)
}

func GetTaskLogsExport(c *gin.Context) {
	start, end, err := parseLogExportRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userID := 0
	if c.GetInt("role") < common.RoleAdminUser {
		userID = c.GetInt("id")
	}
	rows, total, err := model.ExportTaskLogs(c.Request.Context(), userID, start, end, c.Query("task_id"), c.Query("channel_id"), common.MaxLogExportRows)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	xlsxRows := make([][]any, 0, len(rows))
	for _, row := range rows {
		xlsxRows = append(xlsxRows, []any{row.ID, row.UserID, row.TaskID, row.Platform, row.Action, row.Status, row.Group, row.Quota, row.SubmitTime, common.BeijingISOTime(row.SubmitTime), row.StartTime, row.FinishTime, row.Progress, row.FailReason})
	}
	data, err := common.BuildXLSX("Task logs", start, end, total, common.XLSXSheet{Name: "任务日志", Headers: []string{"ID", "User ID", "Task ID", "Platform", "Action", "Status", "Group", "Quota", "Submit Time", "Submit Time (Beijing)", "Start Time", "Finish Time", "Progress", "Fail Reason"}, Rows: xlsxRows})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	writeXLSX(c, "task-logs.xlsx", data)
}

func GetDrawingLogsExport(c *gin.Context) {
	start, end, err := parseLogExportRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userID := 0
	if c.GetInt("role") < common.RoleAdminUser {
		userID = c.GetInt("id")
	}
	rows, total, err := model.ExportDrawingLogs(c.Request.Context(), userID, start, end, c.Query("mj_id"), c.Query("channel_id"), common.MaxLogExportRows)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	xlsxRows := make([][]any, 0, len(rows))
	for _, row := range rows {
		xlsxRows = append(xlsxRows, []any{row.ID, row.UserID, row.MJID, row.Action, row.Status, row.Progress, row.SubmitTime, common.BeijingISOTime(row.SubmitTime), row.StartTime, row.FinishTime, row.Quota, row.FailReason})
	}
	data, err := common.BuildXLSX("Drawing logs", start, end, total, common.XLSXSheet{Name: "绘图日志", Headers: []string{"ID", "User ID", "MJ ID", "Action", "Status", "Progress", "Submit Time", "Submit Time (Beijing)", "Start Time", "Finish Time", "Quota", "Fail Reason"}, Rows: xlsxRows})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	writeXLSX(c, "drawing-logs.xlsx", data)
}

func writeXLSX(c *gin.Context, filename string, data []byte) {
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data)
}
