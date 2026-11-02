package service

import (
	"context"
	"crypto/subtle"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v "micro-one-api/api/admin/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/platform/audit"
)

func (s *AdminService) SetAnnouncementUsecase(u *biz.AnnouncementUsecase) { s.announcementUc = u }
func (s *AdminService) announcementSubject(ctx context.Context) (biz.AnnouncementSubject, error) {
	raw := operatorCredential(ctx)
	if raw == "" {
		return biz.AnnouncementSubject{}, status.Error(codes.Unauthenticated, "user session required")
	}
	id, err := s.AuthenticateSelf(ctx, raw)
	if err != nil {
		return biz.AnnouncementSubject{}, err
	}
	if s.identityClient == nil {
		return biz.AnnouncementSubject{}, biz.ErrAnnouncementUnavailable
	}
	reply, err := s.identityClient.GetUserRoutingFacts(operatorRPCContext(WithOperatorCredential(ctx, "")), &identityv1.GetUserRoutingFactsRequest{UserId: id})
	if err != nil {
		return biz.AnnouncementSubject{}, err
	}
	if reply.GetFacts() == nil || reply.Facts.AccessRevision <= 0 {
		return biz.AnnouncementSubject{}, biz.ErrAnnouncementUnavailable
	}
	return biz.AnnouncementSubject{UserID: id, GroupID: reply.Facts.DefaultGroupId}, nil
}
func (s *AdminService) ListManagedAnnouncements(ctx context.Context, req *v.ListAnnouncementsRequest) (*v.ListAnnouncementsReply, error) {
	items, total, err := s.announcementUc.Managed(ctx, req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, err
	}
	out := &v.ListAnnouncementsReply{Total: total, Items: make([]*v.Announcement, 0, len(items))}
	for _, a := range items {
		out.Items = append(out.Items, &v.Announcement{Id: a.ID, Revision: a.Revision, Title: a.Title, Body: a.Body, Audience: a.Audience, Targets: a.Targets, Status: a.Status, PublishAt: a.PublishAt, ExpiresAt: a.ExpiresAt, UpdatedAt: a.UpdatedAt})
	}
	return out, nil
}
func (s *AdminService) SaveAnnouncement(ctx context.Context, req *v.SaveAnnouncementRequest) (*v.Announcement, error) {
	if req == nil || req.Announcement == nil {
		return nil, biz.ErrAnnouncementInvalid
	}
	p := req.Announcement
	a := biz.Announcement{ID: p.Id, Revision: p.Revision, Title: p.Title, Body: p.Body, Audience: p.Audience, Targets: p.Targets, Status: p.Status, PublishAt: p.PublishAt, ExpiresAt: p.ExpiresAt}
	// The actor is derived from the verified session; payloads never select it.
	actor := audit.ActorFrom(ctx)
	actorID := fmt.Sprint(actor.UserID)
	if actor.UserID <= 0 {
		id, err := s.AuthenticateSelf(ctx, operatorCredential(ctx))
		if err != nil {
			configured := os.Getenv("ADMIN_TOKEN")
			raw := operatorCredential(ctx)
			if configured == "" || raw == "" || subtle.ConstantTimeCompare([]byte(configured), []byte(raw)) != 1 {
				return nil, err
			}
			actorID = "system/admin-token"
		} else {
			actorID = fmt.Sprint(id)
		}
	}
	a, err := s.announcementUc.Save(ctx, a, req.Reason, actorID)
	if err != nil {
		return nil, err
	}
	return &v.Announcement{Id: a.ID, Revision: a.Revision, Title: a.Title, Body: a.Body, Audience: a.Audience, Targets: a.Targets, Status: a.Status, PublishAt: a.PublishAt, ExpiresAt: a.ExpiresAt, UpdatedAt: a.UpdatedAt}, nil
}
func (s *AdminService) ListMyAnnouncements(ctx context.Context, req *v.ListAnnouncementsRequest) (*v.ListMyAnnouncementsReply, error) {
	subject, err := s.announcementSubject(ctx)
	if err != nil {
		return nil, err
	}
	items, total, err := s.announcementUc.Mine(ctx, subject, req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, err
	}
	out := &v.ListMyAnnouncementsReply{Total: total, Items: make([]*v.UserAnnouncement, 0, len(items))}
	for _, a := range items {
		out.Items = append(out.Items, &v.UserAnnouncement{Id: a.ID, Revision: a.Revision, Title: a.Title, Body: a.Body, PublishAt: a.PublishAt, ExpiresAt: a.ExpiresAt, Read: a.Read})
	}
	return out, nil
}
func (s *AdminService) ReadAnnouncement(ctx context.Context, req *v.ReadAnnouncementRequest) (*v.ReadAnnouncementReply, error) {
	if req == nil {
		return nil, biz.ErrAnnouncementInvalid
	}
	subject, err := s.announcementSubject(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.announcementUc.Read(ctx, req.Id, req.Revision, subject); err != nil {
		return nil, err
	}
	return &v.ReadAnnouncementReply{Success: true}, nil
}
func (s *AdminService) GetMyModelAvailability(ctx context.Context, _ *v.GetMyModelAvailabilityRequest) (*v.MyModelAvailability, error) {
	raw := operatorCredential(ctx)
	if raw == "" {
		return nil, status.Error(codes.Unauthenticated, "user session required")
	}
	if _, err := s.AuthenticateSelf(ctx, raw); err != nil {
		return nil, err
	}
	models, err := s.AvailableModels(ctx, raw, "")
	if err != nil {
		return nil, err
	}
	state := "available"
	if len(models) == 0 {
		state = "no_available_route"
	}
	return &v.MyModelAvailability{Models: models, Status: state, CheckedAt: time.Now().Unix()}, nil
}
