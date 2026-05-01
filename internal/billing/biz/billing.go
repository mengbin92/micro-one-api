package biz

// Reservation is the minimal billing reservation model for phase one.
type Reservation struct {
	RequestID     string
	ReservationID string
	UserID        int64
	TokenID       int64
	Quota         int64
}
