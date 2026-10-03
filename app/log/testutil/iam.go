// Package testutil exposes owner construction for cross-service IAM tests.
package testutil

import (
	"gorm.io/gorm"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/app/log/internal/data"
	"micro-one-api/app/log/internal/server"
	"micro-one-api/app/log/internal/service"
	"micro-one-api/domain/authorization"
	"net/http"
)

func NewIAMHTTP(db *gorm.DB, resolver authorization.Resolver) http.Handler {
	r := data.NewRepositoryWithDB(db)
	s := service.NewLogService(biz.NewLogUsecase(r))
	s.SetAuthorization(resolver)
	return server.NewHTTPServer(":0", s)
}
