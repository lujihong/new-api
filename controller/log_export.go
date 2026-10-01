package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
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

func exportTaskStatusLabel(status string) string {
	switch status {
	case "SUCCESS":
		return "成功"
	case "FAILURE":
		return "失败"
	case "IN_PROGRESS":
		return "进行中"
	case "QUEUED", "SUBMITTED", "NOT_START":
		return "排队中"
	default:
		return status
	}
}

func exportTaskActionLabel(action string) string {
	labels := map[string]string{"text_to_video": "文生视频", "image_to_video": "图生视频", "text_to_image": "文生图", "music": "音乐生成", "lyrics": "歌词", "description": "描述"}
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
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func exportCommonRow(log *model.Log, isAdmin, isRoot bool) []any {
	other := exportOtherMap(log.Other)
	publicTaskID := exportString(other["task_id"])
	requestPath := ""
	if audit, ok := other["audit_info"].(map[string]any); ok {
		requestPath = exportString(audit["path"])
		if requestPath == "" {
			requestPath = exportString(audit["route"])
		}
	}
	adminInfo, _ := other["admin_info"].(map[string]any)
	if requestPath == "" && isAdmin {
		requestPath = exportString(other["request_path"])
	}
	cacheRead := exportString(other["cache_tokens"])
	cacheWrite := exportString(other["cache_creation_tokens"])
	if cacheWrite == "" {
		cacheWrite = exportString(other["cache_creation_tokens_5m"])
	}
	detail := exportCommonDetail(log, other, isAdmin)
	row := []any{
		common.BeijingDateTime(log.CreatedAt),
		exportLogTypeLabel(log.Type),
		log.TokenName,
		log.ModelName,
		log.Group,
		log.PromptTokens,
		log.CompletionTokens,
		cacheRead,
		cacheWrite,
		exportDuration(log.UseTime),
		map[bool]string{true: "是", false: "否"}[log.IsStream],
		exportQuota(log.Quota, other),
		log.RequestId,
		log.UpstreamRequestId,
		publicTaskID,
		detail,
		requestPath,
	}
	if isAdmin {
		row = append(row, log.Username, log.ChannelName, log.ChannelId, log.Ip)
		if adminInfo != nil {
			row = append(row, exportString(adminInfo["usage_billing_path"]), exportString(adminInfo["reject_reason"]))
		} else {
			row = append(row, "", "")
		}
	}
	if isRoot {
		rootInfo, _ := other["root_info"].(map[string]any)
		row = append(row, exportString(rootInfo["upstream_task_id"]), exportString(rootInfo["node_name"]))
	}
	return row
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
	if exportString(other["billing_source"]) == "subscription" {
		return "订阅扣费 " + logger.FormatQuota(quota)
	}
	return logger.FormatQuota(quota)
}

func exportCommonDetail(log *model.Log, other map[string]any, isAdmin bool) string {
	parts := make([]string, 0, 10)
	if log.Content != "" {
		parts = append(parts, "内容："+log.Content)
	}
	if value := exportString(other["reason"]); value != "" {
		parts = append(parts, "原因："+value)
	}
	if value := exportString(other["matched_tier"]); value != "" {
		parts = append(parts, "匹配档位："+value)
	}
	if mapped, ok := other["is_model_mapped"].(bool); ok && mapped {
		if value := exportString(other["upstream_model_name"]); value != "" {
			parts = append(parts, "实际模型："+value)
		}
	}
	if other["is_system_prompt_overwritten"] == true {
		parts = append(parts, "系统提示词：已覆盖")
	}
	if value := exportString(other["stream_status"]); value != "" {
		parts = append(parts, "流式状态："+value)
	}
	if value := exportString(other["audio_input"]); value != "" {
		parts = append(parts, "音频输入："+value)
	}
	if value := exportString(other["audio_output"]); value != "" {
		parts = append(parts, "音频输出："+value)
	}
	if value := exportString(other["reasoning_effort"]); value != "" {
		parts = append(parts, "推理强度："+value)
	}
	if isAdmin {
		if value := exportString(other["request_conversion"]); value != "" {
			parts = append(parts, "请求转换："+value)
		}
		if value := exportString(other["billing_mode"]); value != "" {
			parts = append(parts, "计费模式："+value)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "；")
}

func commonExportHeaders(isAdmin, isRoot bool) []string {
	headers := []string{"时间", "日志类型", "令牌名称", "模型", "分组", "输入词元数", "输出词元数", "缓存读取词元数", "缓存写入词元数", "响应耗时", "流式", "费用", "请求标识", "上游请求标识", "任务标识", "详情", "请求路径"}
	if isAdmin {
		headers = append(headers, "用户", "渠道名称", "渠道编号", "IP地址", "计费路径", "拦截原因")
	}
	if isRoot {
		headers = append(headers, "上游任务标识", "节点名称")
	}
	return headers
}

func GetLogsExport(c *gin.Context) {
	start, end, err := parseLogExportRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	role := c.GetInt("role")
	userID := 0
	if role < common.RoleAdminUser || c.Query("scope") == "self" {
		userID = c.GetInt("id")
	}
	logType, _ := strconv.Atoi(c.Query("type"))
	filter := model.LogExportFilter{UserID: userID, Start: start, End: end, LogType: logType, ModelName: c.Query("model_name"), Username: c.Query("username"), TokenName: c.Query("token_name"), Group: c.Query("group"), RequestID: c.Query("request_id"), UpstreamRequestID: c.Query("upstream_request_id")}
	filter.Channel, _ = strconv.Atoi(c.Query("channel"))
	total, err := model.CountExportLogs(c.Request.Context(), filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if total > common.MaxLogExportRows {
		common.ApiError(c, fmt.Errorf("export contains %d rows; maximum is %d", total, common.MaxLogExportRows))
		return
	}
	isAdmin, isRoot := role >= common.RoleAdminUser, role >= common.RoleRootUser
	headers := commonExportHeaders(isAdmin, isRoot)
	if err := writeXLSXStreamFile(c, "usage-logs.xlsx", "API usage logs", start, end, total, common.XLSXStreamSheet{Name: "日志", Headers: headers, WriteRows: func(writer *excelize.StreamWriter) error {
		beforeID, rowNo := 0, 2
		for {
			logs, hasMore, _, pageErr := model.ExportLogsPage(c.Request.Context(), filter, beforeID, model.ExportPageSize, false)
			if pageErr != nil {
				return pageErr
			}
			for _, log := range logs {
				if err := writer.SetRow(excelizeCell(rowNo), exportCommonRow(log, isAdmin, isRoot)); err != nil {
					return err
				}
				rowNo++
			}
			if len(logs) == 0 || !hasMore {
				break
			}
			beforeID = logs[len(logs)-1].Id
		}
		return nil
	}}); err != nil {
		common.ApiError(c, err)
	}
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
	taskID, channelID := c.Query("task_id"), c.Query("channel_id")
	total, err := model.CountTaskLogs(c.Request.Context(), userID, start, end, taskID, channelID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if total > common.MaxLogExportRows {
		common.ApiError(c, fmt.Errorf("export contains %d rows; maximum is %d", total, common.MaxLogExportRows))
		return
	}
	role := c.GetInt("role")
	headers := []string{"提交时间", "开始时间", "完成时间", "任务标识", "平台", "操作类型", "状态", "进度", "任务耗时", "原始模型", "实际模型", "失败原因"}
	if role >= common.RoleAdminUser {
		headers = append(headers, "用户", "渠道编号", "分组", "费用", "请求标识", "请求路径", "插件名称", "插件版本", "插件作者")
	}
	if role >= common.RoleRootUser {
		headers = append(headers, "上游任务标识", "节点名称", "接口版本", "插件代次")
	}
	if err := writeXLSXStreamFile(c, "task-logs.xlsx", "Task logs", start, end, total, common.XLSXStreamSheet{Name: "任务日志", Headers: headers, WriteRows: func(writer *excelize.StreamWriter) error {
		beforeID, rowNo := int64(0), 2
		for {
			rows, more, pageErr := model.ExportTaskLogsPage(c.Request.Context(), userID, start, end, taskID, channelID, beforeID, model.ExportPageSize)
			if pageErr != nil {
				return pageErr
			}
			for _, row := range rows {
				values := []any{common.BeijingDateTime(row.SubmitTime), common.BeijingDateTime(row.StartTime), common.BeijingDateTime(row.FinishTime), row.TaskID, row.Platform, exportTaskActionLabel(row.Action), exportTaskStatusLabel(row.Status), row.Progress, exportDuration(int(row.FinishTime - row.SubmitTime)), row.OriginModel, row.ActualModel, row.FailReason}
				if role >= common.RoleAdminUser {
					values = append(values, strconv.Itoa(row.UserID), row.ChannelID, row.Group, exportQuota(row.Quota, nil), row.RequestID, row.RequestPath, row.PluginName, row.PluginVersion, row.PluginAuthor)
				}
				if role >= common.RoleRootUser {
					values = append(values, row.UpstreamTaskID, row.NodeName, row.APIVersion, row.PluginGeneration)
				}
				if err := writer.SetRow(excelizeCell(rowNo), values); err != nil {
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
	}}); err != nil {
		common.ApiError(c, err)
	}
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
	mjID, channelID := c.Query("mj_id"), c.Query("channel_id")
	total, err := model.CountDrawingLogs(c.Request.Context(), userID, start, end, mjID, channelID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if total > common.MaxLogExportRows {
		common.ApiError(c, fmt.Errorf("export contains %d rows; maximum is %d", total, common.MaxLogExportRows))
		return
	}
	role := c.GetInt("role")
	headers := []string{"提交时间", "操作类型", "状态", "任务标识", "进度", "任务耗时", "图片地址", "提示词", "英文提示词", "失败原因"}
	if role >= common.RoleAdminUser {
		headers = append(headers, "渠道编号", "费用", "提交结果")
	}
	if err := writeXLSXStreamFile(c, "drawing-logs.xlsx", "Drawing logs", start, end, total, common.XLSXStreamSheet{Name: "绘图日志", Headers: headers, WriteRows: func(writer *excelize.StreamWriter) error {
		beforeID, rowNo := 0, 2
		for {
			rows, more, pageErr := model.ExportDrawingLogsPage(c.Request.Context(), userID, start, end, mjID, channelID, beforeID, model.ExportPageSize)
			if pageErr != nil {
				return pageErr
			}
			for _, row := range rows {
				values := []any{common.BeijingDateTime(row.SubmitTime), exportTaskActionLabel(row.Action), exportTaskStatusLabel(row.Status), row.MJID, row.Progress, exportDuration(int(row.FinishTime - row.SubmitTime)), row.ImageURL, row.Prompt, row.PromptEN, row.FailReason}
				if role >= common.RoleAdminUser {
					values = append(values, row.ChannelID, exportQuota(row.Quota, nil), row.Code)
				}
				if err := writer.SetRow(excelizeCell(rowNo), values); err != nil {
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
	}}); err != nil {
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
