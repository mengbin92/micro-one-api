package main

import (
	"fmt"
	b "micro-one-api/domain/backup/biz"
	d "micro-one-api/domain/backup/data"
	"micro-one-api/platform/database/xdb"
	"os"
)

// The host worker has orchestration/S3 credentials. admin-api receives only
// the control DB connection and never a Docker socket or artifact credentials.
func newBackupRepo(cfg *Config) (b.Repo, func(), error) {
	if os.Getenv("BACKUP_CONTROL_ENABLED") != "true" {
		return nil, func() {}, nil
	}
	if cfg.Bootstrap == nil || cfg.Bootstrap.Data == nil || cfg.Bootstrap.Data.Database == nil {
		return nil, nil, fmt.Errorf("backup control DB required")
	}
	c := cfg.Bootstrap.Data.Database
	gdb, err := xdb.Open(xdb.DatabaseConfig{Driver: c.Driver, DSN: resolveAdminSchemaDSN(c.Driver, c.Source)})
	if err != nil {
		return nil, nil, err
	}
	pool, err := gdb.DB()
	if err != nil {
		return nil, nil, err
	}
	return d.NewRepo(&d.Data{DB: gdb}), func() { _ = pool.Close() }, nil
}
