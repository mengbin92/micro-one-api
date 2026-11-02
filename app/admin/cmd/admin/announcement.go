package main

import (
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data"
	"micro-one-api/platform/database/xdb"
)

func newAnnouncementRepo(cfg *Config) (biz.AnnouncementRepo, func(), error) {
	if cfg.Bootstrap == nil || cfg.Bootstrap.Data == nil || cfg.Bootstrap.Data.Database == nil || cfg.Bootstrap.Data.Database.Source == "" {
		return nil, func() {}, nil
	}
	c := cfg.Bootstrap.Data.Database
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: c.Driver, DSN: resolveAdminSchemaDSN(c.Driver, c.Source)})
	if err != nil {
		return nil, nil, err
	}
	pool, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	return data.NewAnnouncementRepo(&data.Data{DB: db}), func() { _ = pool.Close() }, nil
}
