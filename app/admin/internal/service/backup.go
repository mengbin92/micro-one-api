package service

import (
	"context"
	"fmt"
	v "micro-one-api/api/admin/v1"
	b "micro-one-api/domain/backup/biz"
	"micro-one-api/platform/audit"
)

func (s *AdminService) SetBackupUsecase(u *b.Usecase) { s.backupUc = u }
func (s *AdminService) backupActor(ctx context.Context) string {
	a := audit.ActorFrom(ctx)
	if a.UserID > 0 {
		return fmt.Sprint(a.UserID)
	}
	if id, role, err := s.AuthorizeAdminToken(ctx, operatorCredential(ctx)); err == nil && role >= RoleAdmin {
		return fmt.Sprint(id)
	}
	return "system/admin-token"
}
func convertBackupPolicy(p *v.BackupPolicy) (b.Policy, error) {
	if p == nil {
		return b.Policy{}, b.ErrInvalid
	}
	out := b.Policy{Revision: p.Revision, Enabled: p.Enabled, IntervalHours: p.IntervalHours, Retain: p.Retain, RPOHours: p.RpoHours, RTOMinutes: p.RtoMinutes}
	return out, out.Validate()
}
func (s *AdminService) GetBackupPolicy(ctx context.Context, _ *v.GetBackupPolicyRequest) (*v.BackupPolicy, error) {
	p, err := s.backupUc.Policy(ctx)
	if err != nil {
		return nil, err
	}
	return &v.BackupPolicy{Revision: p.Revision, Enabled: p.Enabled, IntervalHours: p.IntervalHours, Retain: p.Retain, RpoHours: p.RPOHours, RtoMinutes: p.RTOMinutes}, nil
}
func (s *AdminService) UpdateBackupPolicy(ctx context.Context, req *v.UpdateBackupPolicyRequest) (*v.BackupPolicy, error) {
	if req == nil {
		return nil, b.ErrInvalid
	}
	p, err := convertBackupPolicy(req.Policy)
	if err != nil {
		return nil, err
	}
	p, err = s.backupUc.UpdatePolicy(ctx, p, s.backupActor(ctx))
	if err != nil {
		return nil, err
	}
	return &v.BackupPolicy{Revision: p.Revision, Enabled: p.Enabled, IntervalHours: p.IntervalHours, Retain: p.Retain, RpoHours: p.RPOHours, RtoMinutes: p.RTOMinutes}, nil
}
func (s *AdminService) CreateBackup(ctx context.Context, req *v.CreateBackupRequest) (*v.BackupJob, error) {
	if req == nil {
		return nil, b.ErrInvalid
	}
	j, err := s.backupUc.Create(ctx, req.RequestId, s.backupActor(ctx))
	if err != nil {
		return nil, err
	}
	return &v.BackupJob{Id: j.ID, Status: j.Status, Trigger: j.Trigger, Actor: j.Actor, Reason: j.Reason, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, Sha256: j.SHA256, Bytes: j.Bytes, ErrorCode: j.ErrorCode}, nil
}
func (s *AdminService) ListBackups(ctx context.Context, _ *v.ListBackupsRequest) (*v.ListBackupsReply, error) {
	jobs, err := s.backupUc.List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := s.backupUc.Events(ctx)
	if err != nil {
		return nil, err
	}
	worker, err := s.backupUc.WorkerState(ctx)
	if err != nil {
		return nil, err
	}
	out := &v.ListBackupsReply{WorkerLastSeen: worker.LastSeen, WorkerPhase: worker.Phase, WorkerErrorCode: worker.ErrorCode, WorkerTimeoutMinutes: worker.TimeoutMinutes}
	for _, j := range jobs {
		p := j.Policy
		out.Jobs = append(out.Jobs, &v.BackupJob{Id: j.ID, Status: j.Status, Trigger: j.Trigger, Actor: j.Actor, Reason: j.Reason, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, Sha256: j.SHA256, Bytes: j.Bytes, ErrorCode: j.ErrorCode, Policy: &v.BackupPolicy{Revision: p.Revision, Enabled: p.Enabled, IntervalHours: p.IntervalHours, Retain: p.Retain, RpoHours: p.RPOHours, RtoMinutes: p.RTOMinutes}})
	}
	for _, e := range events {
		out.Events = append(out.Events, &v.BackupEvent{Id: e.ID, JobId: e.JobID, Action: e.Action, Actor: e.Actor, Reason: e.Reason, At: e.At})
	}
	return out, nil
}
