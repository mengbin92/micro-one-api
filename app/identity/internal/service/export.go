package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	identityv1 "micro-one-api/api/identity/v1"
	"strconv"
	"strings"
)

func exportUserCell(s string) string {
	v := strings.TrimLeft(s, " \t\r\n")
	if len(v) > 0 && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + s
	}
	return s
}
func (s *IdentityService) ExportUsers(ctx context.Context, req *identityv1.ExportUsersRequest) (*identityv1.ExportUsersReply, error) {
	ctx, err := s.managedIdentityContext(ctx)
	if err != nil {
		return nil, err
	}
	q := req.GetQuery()
	if q == nil || q.Page < 1 || q.PageSize < 1 || q.PageSize > 200 {
		return nil, status.Error(codes.InvalidArgument, "explicit export page and page_size <= 200 required")
	}
	users, _, err := s.uc.ExportManagedUsers(ctx, q.Page, q.PageSize, q.Keyword, q.Group, q.Status)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	var buf bytes.Buffer
	out := csv.NewWriter(&buf)
	if err = out.Write([]string{"id", "username", "display_name", "email", "group", "status"}); err != nil {
		return nil, err
	}
	for _, u := range users {
		if err = out.Write([]string{strconv.FormatInt(u.ID, 10), exportUserCell(u.Username), exportUserCell(u.DisplayName), exportUserCell(u.Email), exportUserCell(u.Group), strconv.FormatInt(int64(u.Status), 10)}); err != nil {
			return nil, err
		}
	}
	out.Flush()
	if err = out.Error(); err != nil {
		return nil, err
	}
	return &identityv1.ExportUsersReply{ContentType: "text/csv; charset=utf-8", FileName: "admin-users.csv", Body: buf.Bytes()}, nil
}
