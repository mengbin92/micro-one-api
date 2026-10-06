// Package testutil exposes owner construction for cross-service IAM tests.
package testutil

import (
	"gorm.io/gorm"
	"micro-one-api/app/notify/internal/biz"
	"micro-one-api/app/notify/internal/data"
	"micro-one-api/app/notify/internal/server"
	"micro-one-api/app/notify/internal/service"
	"micro-one-api/domain/authorization"
	"net/http"
)

func NewIAMHTTP(db *gorm.DB, resolver authorization.Resolver) http.Handler {
	r := data.NewRepositoryWithDB(db)
	s := service.NewNotifyService(biz.NewNotifyUsecase(r), "", "")
	s.SetAuthorization(resolver)
	return server.NewHTTPServer(":0", s)
}
