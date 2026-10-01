package model

import (
	"context"
	"errors"
	"gorm.io/gorm"
)

// SnapshotExportLogs freezes the real keyset upper bound before counting.
// Each aggregate runs on a clone so SELECT(MAX) cannot contaminate row queries.
func SnapshotExportLogs(ctx context.Context, f LogExportFilter) (int, int64, error) {
	q, err := queryExportLogs(ctx, f)
	if err != nil {
		return 0, 0, err
	}
	var id int
	if err = q.Session(&gorm.Session{}).Select("COALESCE(MAX(id), 0)").Scan(&id).Error; err != nil {
		return 0, 0, err
	}
	var total int64
	if id > 0 {
		err = q.Session(&gorm.Session{}).Where("id <= ?", id).Count(&total).Error
	}
	return id, total, err
}

type exportSnapshotKey struct{}

func WithExportSnapshot(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, exportSnapshotKey{}, id)
}
func exportSnapshotQuery(q *gorm.DB, ctx context.Context) *gorm.DB {
	if id, ok := ctx.Value(exportSnapshotKey{}).(int64); ok {
		return q.Where("id <= ?", id)
	}
	return q
}
func SnapshotTaskExport(ctx context.Context, userID int, start, end int64, taskID, channelID string, drawing bool) (int64, int64, error) {
	if userID < 0 || start <= 0 || end <= start {
		return 0, 0, errors.New("invalid export range or scope")
	}
	q := DB.WithContext(ctx)
	if drawing {
		q = q.Model(&Midjourney{}).Where("submit_time >= ? AND submit_time < ?", start*1000, end*1000)
		if taskID != "" {
			q = q.Where("mj_id = ?", taskID)
		}
	} else {
		q = q.Model(&Task{}).Where("submit_time >= ? AND submit_time < ?", start, end)
		if taskID != "" {
			q = q.Where("task_id = ?", taskID)
		}
	}
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	if channelID != "" {
		q = q.Where("channel_id = ?", channelID)
	}
	var id, total int64
	if err := q.Session(&gorm.Session{}).Select("COALESCE(MAX(id),0)").Scan(&id).Error; err != nil {
		return 0, 0, err
	}
	if err := q.Session(&gorm.Session{}).Where("id <= ?", id).Count(&total).Error; err != nil {
		return 0, 0, err
	}
	return id, total, nil
}
