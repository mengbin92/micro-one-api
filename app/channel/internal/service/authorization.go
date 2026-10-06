package service

import (
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
)

func (s *ChannelService) SetResourceAuthorization(c *authz.Client) {
	s.authz = c
	s.uc.SetAuthorization(c)
	if s.routingUC != nil {
		s.routingUC.SetAuthorization(c)
	}
	if s.modelUC != nil {
		s.modelUC.SetAuthorization(c)
	}
	if uc, ok := s.routingGroupUC.(interface{ SetAuthorization(authorization.Resolver) }); ok {
		uc.SetAuthorization(c)
	}
}
func (s *ChannelService) SetResourceAuthorizer(r authorization.Resolver) {
	s.authz = r
	s.uc.SetAuthorization(r)
	if s.routingUC != nil {
		s.routingUC.SetAuthorization(r)
	}
	if s.modelUC != nil {
		s.modelUC.SetAuthorization(r)
	}
	if uc, ok := s.routingGroupUC.(interface{ SetAuthorization(authorization.Resolver) }); ok {
		uc.SetAuthorization(r)
	}
}
func (s *ChannelService) OwnerAuthorizationClient() *authz.Client {
	c, _ := s.authz.(*authz.Client)
	return c
}
