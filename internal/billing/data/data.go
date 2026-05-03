package data

import (
	"context"
	"os"

	"micro-one-api/internal/billing/biz"
	"micro-one-api/internal/pkg/xdb"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Data struct {
	db    *gorm.DB
	redis *redis.Client

	accountRepo     biz.AccountRepo
	reservationRepo biz.ReservationRepo
	ledgerRepo      biz.LedgerRepo
	redeemRepo      biz.RedeemRepo
}

func NewData(dsn ...string) (*Data, error) {
	// If DSN is provided via config, use it; otherwise fall back to env vars.
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("BILLING_SQL_DSN")
		if dbDSN == "" {
			dbDSN = os.Getenv("SQL_DSN")
		}
	}

	db, err := xdb.OpenMySQL(dbDSN)
	if err != nil {
		return nil, err
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	rdb := xdb.NewRedisClient(redisAddr)
	if rdb != nil {
		if pingErr := xdb.PingRedis(context.Background(), rdb); pingErr != nil {
			rdb.Close()
			rdb = nil
		}
	}

	d := &Data{
		db:    db,
		redis: rdb,
	}

	d.accountRepo = NewAccountRepo(d)
	d.reservationRepo = NewReservationRepo(d)
	d.ledgerRepo = NewLedgerRepo(d)
	d.redeemRepo = NewRedeemRepo(d)

	return d, nil
}

func (d *Data) AccountRepo() biz.AccountRepo {
	return d.accountRepo
}

func (d *Data) ReservationRepo() biz.ReservationRepo {
	return d.reservationRepo
}

func (d *Data) LedgerRepo() biz.LedgerRepo {
	return d.ledgerRepo
}

func (d *Data) RedeemRepo() biz.RedeemRepo {
	return d.redeemRepo
}

func (d *Data) Close() error {
	if d.redis != nil {
		d.redis.Close()
	}
	if d.db != nil {
		sqlDB, err := d.db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}
