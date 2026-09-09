package biz

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/routing"
)

// RoutingGroupReader is the channel-owner boundary, not a cross-schema query.
type RoutingGroupReader interface {
	List(context.Context, routing.GroupListRequest) (*routing.GroupListResult, error)
	Get(context.Context, int64) (*routing.GroupDetail, error)
}
type RoutingGroupUsecase struct{ reader RoutingGroupReader }

func NewRoutingGroupUsecase(reader RoutingGroupReader) *RoutingGroupUsecase {
	return &RoutingGroupUsecase{reader: reader}
}
func (uc *RoutingGroupUsecase) List(ctx context.Context, query routing.GroupListRequest) (*routing.GroupListResult, error) {
	return uc.reader.List(ctx, query)
}
func (uc *RoutingGroupUsecase) Get(ctx context.Context, id int64) (*routing.GroupDetail, error) {
	return uc.reader.Get(ctx, id)
}

var (
	ErrRoutingGroupInvalid     = errors.BadRequest(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_INVALID.String(), "无效的分组查询")
	ErrRoutingGroupUnavailable = errors.ServiceUnavailable(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_STORAGE_UNAVAILABLE.String(), "分组服务暂不可用")
)
