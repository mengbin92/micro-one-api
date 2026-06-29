package biz

import "time"

const (
	ReservationStatusReserved = "reserved"
	ReservationStatusCommitted = "committed"
	ReservationStatusReleased  = "released"
	ReservationStatusExpired   = "expired"
)

type Reservation struct {
	ReservationID string
	UserID       string
	RequestID    string
	Amount       int64
	Status       string
	Model        string
	ChannelID              string
	SubscriptionAccountID string
	CreatedAt             time.Time
	UpdatedAt    time.Time
	ExpiredAt    time.Time
}

func (r *Reservation) IsExpired() bool {
	return r.ExpiredAt.Before(time.Now())
}

func (r *Reservation) IsReserved() bool {
	return r.Status == ReservationStatusReserved
}

func (r *Reservation) IsCommitted() bool {
	return r.Status == ReservationStatusCommitted
}

func (r *Reservation) IsReleased() bool {
	return r.Status == ReservationStatusReleased
}
