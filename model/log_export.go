package model

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

const (
	TokenLogPageDefaultLimit = 500
	TokenLogPageMaxLimit     = 1000
)

type TokenLogPage struct {
	Items        []*Log `json:"items"`
	Total        int64  `json:"total"`
	SnapshotID   int    `json:"snapshot_id"`
	NextBeforeID int    `json:"next_before_id"`
	HasMore      bool   `json:"has_more"`
}

// GetTokenLogPage is a read-only, keyset-paginated query. It deliberately
// receives both userID and tokenID so callers cannot widen a token's scope.
func GetTokenLogPage(ctx context.Context, userID, tokenID int, start, end int64, limit, snapshotID, beforeID int) (TokenLogPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if userID <= 0 || tokenID <= 0 {
		return TokenLogPage{}, errors.New("invalid token scope")
	}
	if limit <= 0 {
		limit = TokenLogPageDefaultLimit
	}
	if limit > TokenLogPageMaxLimit {
		return TokenLogPage{}, fmt.Errorf("limit must be at most %d", TokenLogPageMaxLimit)
	}
	if end <= start || start <= 0 {
		return TokenLogPage{}, errors.New("invalid time range")
	}
	base := LOG_DB.WithContext(ctx).Model(&Log{}).
		Where("user_id = ? AND token_id = ? AND created_at >= ? AND created_at < ?", userID, tokenID, start, end)
	if snapshotID <= 0 {
		var maxID int
		if err := base.Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
			return TokenLogPage{}, err
		}
		snapshotID = maxID
	}
	if snapshotID <= 0 {
		return TokenLogPage{Items: []*Log{}, SnapshotID: 0}, nil
	}
	base = base.Where("id <= ?", snapshotID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return TokenLogPage{}, err
	}
	query := base
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	var logs []*Log
	if err := query.Order("id DESC").Limit(limit + 1).Find(&logs).Error; err != nil {
		return TokenLogPage{}, err
	}
	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}
	ids := make([]int, len(logs))
	for i, log := range logs {
		ids[i] = log.Id
	}
	formatUserLogs(logs, 0)
	// formatUserLogs intentionally uses display IDs for legacy dashboard pages.
	// Token cursors must expose the real numeric IDs, so restore them after the
	// protected-field projection.
	for i, log := range logs {
		log.Id = ids[i]
	}
	result := TokenLogPage{Items: logs, Total: total, SnapshotID: snapshotID, HasMore: hasMore}
	if len(logs) > 0 {
		result.NextBeforeID = ids[len(ids)-1]
	}
	return result, nil
}

type LogExportFilter struct {
	UserID            int
	Start             int64
	End               int64
	LogType           int
	ModelName         string
	Username          string
	TokenName         string
	Channel           int
	Group             string
	RequestID         string
	UpstreamRequestID string
}

func queryExportLogs(ctx context.Context, filter LogExportFilter) (*gorm.DB, error) {
	if filter.Start <= 0 || filter.End <= filter.Start {
		return nil, errors.New("invalid time range")
	}
	tx := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at >= ? AND created_at < ?", filter.Start, filter.End)
	if filter.UserID > 0 {
		tx = tx.Where("user_id = ?", filter.UserID)
	}
	if filter.LogType != LogTypeUnknown {
		tx = tx.Where("type = ?", filter.LogType)
	}
	var err error
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", filter.ModelName); err != nil {
		return nil, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "username", filter.Username); err != nil {
		return nil, err
	}
	if filter.TokenName != "" {
		tx = tx.Where("token_name = ?", filter.TokenName)
	}
	if filter.Channel != 0 {
		tx = tx.Where("channel_id = ?", filter.Channel)
	}
	if filter.Group != "" {
		tx = tx.Where("group = ?", filter.Group)
	}
	if filter.RequestID != "" {
		tx = tx.Where("request_id = ?", filter.RequestID)
	}
	if filter.UpstreamRequestID != "" {
		tx = tx.Where("upstream_request_id = ?", filter.UpstreamRequestID)
	}
	return tx, nil
}

func ExportLogs(ctx context.Context, filter LogExportFilter, maxRows int64) ([]*Log, int64, error) {
	query, err := queryExportLogs(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total > maxRows {
		return nil, total, fmt.Errorf("export contains %d rows; maximum is %d", total, maxRows)
	}
	var logs []*Log
	if err = query.Order("id DESC").Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	if filter.UserID > 0 {
		formatUserLogs(logs, 0)
	} else {
		FormatAdminLogs(logs)
	}
	return logs, total, nil
}

type TaskExportRow struct {
	ID         int64
	UserID     int
	Username   string
	TaskID     string
	Platform   string
	Action     string
	Status     string
	Group      string
	Quota      int
	SubmitTime int64
	StartTime  int64
	FinishTime int64
	Progress   string
	FailReason string
}

func ExportTaskLogs(ctx context.Context, userID int, start, end int64, taskID string, channelID string, maxRows int64) ([]TaskExportRow, int64, error) {
	query := DB.WithContext(ctx).Model(&Task{}).Where("submit_time >= ? AND submit_time < ?", start, end)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if taskID != "" {
		query = query.Where("task_id = ?", taskID)
	}
	if channelID != "" {
		query = query.Where("channel_id = ?", channelID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total > maxRows {
		return nil, total, fmt.Errorf("export contains %d rows; maximum is %d", total, maxRows)
	}
	var tasks []*Task
	if err := query.Order("id DESC").Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]TaskExportRow, 0, len(tasks))
	for _, task := range tasks {
		rows = append(rows, TaskExportRow{ID: task.ID, UserID: task.UserId, TaskID: task.TaskID, Platform: string(task.Platform), Action: task.Action, Status: string(task.Status), Group: task.Group, Quota: task.Quota, SubmitTime: task.SubmitTime, StartTime: task.StartTime, FinishTime: task.FinishTime, Progress: task.Progress, FailReason: task.FailReason})
	}
	return rows, total, nil
}

type DrawingExportRow struct {
	ID         int
	UserID     int
	MJID       string
	Action     string
	Status     string
	Progress   string
	SubmitTime int64
	StartTime  int64
	FinishTime int64
	Quota      int
	FailReason string
}

func ExportDrawingLogs(ctx context.Context, userID int, start, end int64, mjID string, channelID string, maxRows int64) ([]DrawingExportRow, int64, error) {
	startMS, endMS := start*1000, end*1000
	query := DB.WithContext(ctx).Model(&Midjourney{}).Where("submit_time >= ? AND submit_time < ?", startMS, endMS)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if mjID != "" {
		query = query.Where("mj_id = ?", mjID)
	}
	if channelID != "" {
		query = query.Where("channel_id = ?", channelID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total > maxRows {
		return nil, total, fmt.Errorf("export contains %d rows; maximum is %d", total, maxRows)
	}
	var tasks []*Midjourney
	if err := query.Order("id DESC").Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]DrawingExportRow, 0, len(tasks))
	for _, task := range tasks {
		rows = append(rows, DrawingExportRow{ID: task.Id, UserID: task.UserId, MJID: task.MjId, Action: task.Action, Status: task.Status, Progress: task.Progress, SubmitTime: task.SubmitTime / 1000, StartTime: task.StartTime / 1000, FinishTime: task.FinishTime / 1000, Quota: task.Quota, FailReason: task.FailReason})
	}
	return rows, total, nil
}
