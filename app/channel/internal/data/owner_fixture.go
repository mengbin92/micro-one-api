package data

import (
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
)

// NewOwnerRepositoriesWithDB borrows the caller-owned scratch database and
// exposes only the declared domain repository seams.
func NewOwnerRepositoriesWithDB(db *gorm.DB) (biz.ChannelRepo, biz.ModelRepo, biz.ModelRoutingRepo, biz.RoutingGroupRepo) {
	r := &Repository{db: db, routingGroupDualWrite: true, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
	return r, r, r, NewRoutingGroupRepo(r)
}
