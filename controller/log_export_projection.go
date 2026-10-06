package controller

import (
	"encoding/json"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/url"
	"strconv"
	"strings"
)

func resolveLogExportScope(c *gin.Context) (int, bool, bool, error) {
	id, role := c.GetInt("id"), c.GetInt("role")
	if id <= 0 || role < common.RoleCommonUser {
		return 0, false, false, fmt.Errorf("invalid export identity")
	}
	switch c.Query("scope") {
	case "", "self":
		return id, false, false, nil
	case "all":
		if role >= common.RoleAdminUser {
			return 0, true, role >= common.RoleRootUser, nil
		}
	}
	return 0, false, false, fmt.Errorf("invalid export scope")
}
func drawingExportHeaders(admin bool) []string {
	h := []string{"提交时间", "操作类型", "状态", "任务标识", "进度", "任务耗时", "图片地址", "提示词", "英文提示词", "失败原因"}
	if admin {
		h = append(h, "渠道编号", "提交结果")
	}
	return h
}
func drawingExportValues(r model.DrawingExportRow, admin bool) []any {
	actions := map[string]string{"IMAGINE": "绘图", "UPSCALE": "放大", "VIDEO": "视频", "EDITS": "编辑", "VARIATION": "变换", "HIGH_VARIATION": "强变换", "LOW_VARIATION": "弱变换", "PAN": "平移", "DESCRIBE": "图生文", "BLEND": "图混合", "UPLOAD": "上传", "SHORTEN": "缩词", "REROLL": "重绘", "INPAINT": "局部重绘", "SWAP_FACE": "换脸", "ZOOM": "缩放", "CUSTOM_ZOOM": "自定义缩放"}
	action := actions[r.Action]
	if action == "" {
		action = "未知"
	}
	status := exportTaskStatusLabel(r.Status)
	if r.Status == "MODAL" {
		status = "等待中"
	}
	en := ""
	if r.Prompt != "" {
		en = r.PromptEN
	}
	v := []any{common.BeijingDateTime(r.SubmitTime), action, status, r.MJID, r.Progress, exportDuration(int(r.FinishTime - r.SubmitTime)), r.ImageURL, r.Prompt, en, r.FailReason}
	if admin {
		result := map[int]string{0: "未提交", 1: "已提交", 21: "等待中", 22: "重复任务"}[r.Code]
		if result == "" {
			result = "未知"
		}
		v = append(v, r.ChannelID, result)
	}
	return v
}
func exportTaskArtifacts(task *model.Task) (string, error) {
	if task == nil || task.Status != model.TaskStatusSuccess {
		return "", nil
	}
	if string(task.Platform) == "suno" && !taskHasPluginExecution(task) {
		return exportLegacySuno(task), nil
	}
	artifacts, err := projectTaskArtifacts(task)
	if err != nil {
		return "", err
	}
	result := ""
	for _, a := range artifacts {
		u, err := service.BuildTaskArtifactContentURL(task.TaskID, a.Key)
		if err != nil {
			return "", err
		}
		typeLabel := map[string]string{"image": "图片", "video": "视频", "audio": "音频", "file": "文件"}[a.Type]
		result += fmt.Sprintf("产物标识：%s\n类型：%s\n媒体类型：%s\n内容地址：%s\n", a.Key, typeLabel, a.MimeType, u)
	}
	if legacyVideoAvailable(task) {
		u, err := service.BuildTaskArtifactContentURL(task.TaskID, "video")
		if err != nil {
			return "", err
		}
		result += "视频地址：" + u
	}
	return result, nil
}

// Legacy Suno uses AudioClipCard, not the plugin artifact projection. Only
// visible title/tags/duration/media fields are exported; IDs and raw data are not.
func exportLegacySuno(task *model.Task) string {
	var clips []map[string]any
	data := []byte(task.Data)
	var encoded string
	if json.Unmarshal(data, &encoded) == nil {
		data = []byte(encoded)
	}
	if json.Unmarshal(data, &clips) != nil {
		return ""
	}
	var parts []string
	for i, clip := range clips {
		audio, ok := clip["audio_url"].(string)
		if !ok || audio == "" {
			continue
		}
		title := exportString(clip["title"])
		if title == "" {
			title = "未命名"
		}
		desc := fmt.Sprintf("音频%d：%s (%s)", i+1, title, audio)
		duration := exportN(clip, "duration")
		if duration > 0 {
			desc += fmt.Sprintf(" 时长 %d:%02d", int(duration)/60, int(duration)%60)
		}
		parts = append(parts, desc)
	}
	return strings.Join(parts, "\n")
}

func safeExportAuthorURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}
func taskExportRow(row model.TaskExportRow, role int) ([]any, error) {
	artifacts, err := exportTaskArtifacts(row.Task)
	if err != nil {
		return nil, err
	}
	if row.Status == string(model.TaskStatusSuccess) && taskFailReasonIsLegacyResultURL(row.FailReason) {
		row.FailReason = ""
	}
	duration := ""
	if row.SubmitTime > 0 && row.FinishTime > 0 {
		duration = exportDuration(int(row.FinishTime - row.SubmitTime))
	}
	values := []any{
		common.BeijingDateTime(row.SubmitTime), common.BeijingDateTime(row.StartTime), common.BeijingDateTime(row.FinishTime),
		row.TaskID, row.Platform, exportTaskActionLabel(row.Action), exportTaskStatusLabel(row.Status), row.Progress,
		duration, row.OriginModel, row.ActualModel, row.FailReason, artifacts,
	}
	if role >= common.RoleAdminUser {
		values = append(values, exportUsername(row), row.ChannelID, row.Group, exportQuota(row.Quota, nil),
			row.RequestID, row.RequestPath, row.PluginName, row.PluginVersion, row.PluginAuthor, row.PluginKey,
			safeExportAuthorURL(row.PluginAuthorURL))
	}
	if role >= common.RoleRootUser {
		var apiVersion, generation any = "", ""
		if row.Task != nil && taskHasPluginExecution(row.Task) {
			apiVersion, generation = row.APIVersion, row.PluginGeneration
		}
		values = append(values, row.UpstreamTaskID, row.NodeName, apiVersion, generation)
	}
	return values, nil
}

func exportUsername(row model.TaskExportRow) string {
	if row.Username != "" {
		return row.Username
	}
	return strconv.Itoa(row.UserID)
}
func checkExportPage(ctx *gin.Context, count int, before, next int64, more bool, written int64) error {
	if err := ctx.Request.Context().Err(); err != nil {
		return err
	}
	if written+int64(count) > common.MaxLogExportRows {
		return fmt.Errorf("export row limit exceeded")
	}
	if count == 0 {
		if more {
			return fmt.Errorf("export cursor made no progress")
		}
		return nil
	}
	if next <= 0 || (before > 0 && next >= before) {
		return fmt.Errorf("export cursor made no progress")
	}
	return nil
}
