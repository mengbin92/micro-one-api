package data

import (
	"context"
	"micro-one-api/app/billing/internal/biz"
)

func (r *reservationRepo) ListRequestAttempts(ctx context.Context, userID, rootID string, offset, limit int) ([]*biz.Reservation, int64, error) {
	query := r.data.db.WithContext(ctx).Model(&reservationModel{}).Where("user_id = ? AND root_request_id = ?", userID, rootID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []reservationModel
	if err := query.Order("attempt_number ASC, id ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*biz.Reservation, 0, len(rows))
	for i := range rows {
		item, err := reservationFromModel(&rows[i])
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, nil
}
